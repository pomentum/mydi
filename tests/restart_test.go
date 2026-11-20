package mydi_test

import (
	"bufio"
	"encoding/hex"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A device that clocks while stopped and restarts its clock at Start: its Start and its first pulse come in the same millisecond, a few
// milliseconds after its last pulse while stopped (a drum machine over USB: 4, 6 and 14 ms, 2026-11-15). That pulse fell in a slot the
// line had filled, was taken for an echo and refused, and everything sent on was a pulse behind the device for the whole run.

// restartingSong is such a device's clock: stopped pulses at the tempo, then Start and the first pulse gap after the last of them, then
// its pulses from there, then Stop; every time on the system's millisecond stamp, as WinMM gives it, at the middle of its millisecond.
func restartingSong(bpm float64, gap time.Duration, stopped, playing int) (song []heard, played []int64) {
	stamp := func(t float64) int64 { return int64(math.Floor(t/float64(ms)))*ms + ms/2 }
	step := interval(bpm)
	origin := float64(time.Second)
	var last float64
	for k := 0; k < stopped; k++ {
		last = origin + float64(k)*step
		song = append(song, heard{stamp(last), []byte{0xF8}})
	}
	first := last + float64(gap)
	song = append(song, heard{stamp(first), []byte{0xFA}})
	for k := 0; k < playing; k++ {
		at := stamp(first + float64(k)*step)
		song = append(song, heard{at, []byte{0xF8}})
		played = append(played, at)
	}
	return append(song, heard{stamp(first+float64(playing)*step) - 5*ms, []byte{0xFC}}), played
}

func TestADeviceThatRestartsItsClockAtStartIsFollowedFromItsFirstPulse(t *testing.T) {
	for _, gap := range []time.Duration{2, 4, 6, 10, 14, 18} {
		gap *= time.Millisecond
		worst := 0.0
		for seed := int64(1); seed <= max(seeds(), 20); seed++ {
			r := rand.New(rand.NewSource(seed))
			song, played := restartingSong(120, gap, 24*8, 24*8)
			re := withEcho(0)
			room := &loopback{r: r, echoFrom: math.MaxInt64}
			sends := room.run(re, song, nil, played[len(played)-1]+int64(time.Second))
			count, startAt, sent := between(sends, 0xFA)
			if count != len(played) || sent[0] < startAt {
				t.Fatalf("a restart %v after the last pulse, seed %d: %d sent for %d (%d refused), the first %d ns after Start",
					gap, seed, count, len(played), re.Counts().Echoes, sent[0]-startAt)
			}
			for i := 24; i < len(sent); i++ {
				worst = math.Max(worst, math.Abs(float64(sent[i]-played[i])))
			}
		}
		// on the line again from the second beat: within the tolerance the line waits for a pulse, never a pulse off
		if worst > 3e6 {
			t.Fatalf("a restart %v after the last pulse: a pulse after the first beat %.2f ms off the device's", gap, worst/1e6)
		}
		t.Logf("a restart %v after the last pulse: the count kept from its first pulse; after the first beat at most %.2f ms off", gap, worst/1e6)
	}
}

// recorded reads a recording of a device in testdata: each message heard, its time in nanoseconds from the first and its bytes.
func recorded(t *testing.T, name string) []heard {
	t.Helper()
	file, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var song []heard
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		at, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		message, err := hex.DecodeString(fields[1])
		if err != nil {
			t.Fatal(err)
		}
		song = append(song, heard{at + int64(time.Second), message})
	}
	return song
}

func TestADrumMachinesRecordedStartsAreEachFollowedFromTheirFirstPulse(t *testing.T) {
	// a drum machine's own clock, recorded over USB, with three Starts: two whose first pulse came 4 and 6 ms after its last pulse
	// stopped (each refused before, the count a pulse behind) and one 14 ms after
	song := recorded(t, "drum-machine-start-stop.txt")
	// the recording ended while the device played: its end taken as a Stop, as the hub's own Stop ended it
	song = append(song, heard{song[len(song)-1].at + 5*ms, []byte{0xFC}})
	re := withEcho(0)
	room := &loopback{r: rand.New(rand.NewSource(1)), echoFrom: math.MaxInt64}
	sends := room.run(re, append([]heard(nil), song...), nil, song[len(song)-1].at+int64(time.Second))
	// each Start to the next Stop: the device's pulses and the hub's, by place
	type span struct{ heard, sent []int64 }
	spans := func(messages []heard) []span {
		var out []span
		running := false
		for _, m := range messages {
			switch m.m[0] {
			case 0xFA:
				running = true
				out = append(out, span{})
			case 0xFC:
				running = false
			case 0xF8:
				if running {
					out[len(out)-1].heard = append(out[len(out)-1].heard, m.at)
				}
			}
		}
		return out
	}
	source := spans(song)
	var own []heard
	for _, s := range sends {
		own = append(own, heard{s.At, s.Message})
	}
	sent := spans(own)
	if len(source) != 3 || len(sent) != 3 {
		t.Fatalf("%d Starts heard, %d sent", len(source), len(sent))
	}
	for i := range source {
		heardTimes, sentTimes := source[i].heard, sent[i].heard
		if len(heardTimes) != len(sentTimes) {
			t.Fatalf("Start %d: %d pulses heard, %d sent (%d refused)", i+1, len(heardTimes), len(sentTimes), re.Counts().Echoes)
		}
		var off []float64
		worst, worstAt := 0.0, 0
		for k := 24; k < len(heardTimes); k++ {
			d := math.Abs(float64(sentTimes[k] - heardTimes[k]))
			if d > worst {
				worst, worstAt = d, k
			}
			off = append(off, d)
		}
		for k := max(1, worstAt-4); k < min(len(heardTimes), worstAt+3); k++ {
			t.Logf("  pulse %d: %.2f ms after the one before, sent %.2f ms after it", k, float64(heardTimes[k]-heardTimes[k-1])/1e6, float64(sentTimes[k]-heardTimes[k])/1e6)
		}
		sort.Float64s(off)
		if len(off) > 0 && off[len(off)/2] > 2e6 {
			t.Fatalf("Start %d: after the first beat a pulse sent a median %.2f ms off the device's", i+1, off[len(off)/2]/1e6)
		}
		if len(off) > 0 {
			t.Logf("Start %d: %d pulses, the count kept; after the first beat median %.2f ms, at most %.2f ms off the device's",
				i+1, len(heardTimes), off[len(off)/2]/1e6, off[len(off)-1]/1e6)
		}
	}
}
