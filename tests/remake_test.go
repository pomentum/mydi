package mydi_test

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

const ms = int64(time.Millisecond)

// heard is one message of the source, heard at its time.
type heard struct {
	at int64
	m  []byte
}

// transport is a source's song: Start just before its first pulse, its pulses, and Stop just after its last.
func transport(times []int64) []heard {
	return transportTo(times, times[len(times)-1]+5*ms)
}

// transportTo is a source's song with its Stop at this time.
func transportTo(times []int64, stop int64) []heard {
	song := []heard{{times[0] - 5*ms, []byte{0xFA}}}
	for _, t := range times {
		song = append(song, heard{t, []byte{0xF8}})
	}
	return append(song, heard{stop, []byte{0xFC}})
}

func pulses(times []int64) []heard {
	var out []heard
	for _, t := range times {
		out = append(out, heard{t, []byte{0xF8}})
	}
	return out
}

// play plays the source into the re-maker, asking it what is due every millisecond but in the stalls (the output's own, as the hub's
// stalls 35-38 ms with notes, ADR-0003), and answers what it sent, in order.
func play(r *mydi.Remaker, song []heard, end int64, stalled func(int64) bool) []mydi.Send {
	sort.SliceStable(song, func(i, j int) bool { return song[i].at < song[j].at })
	var out []mydi.Send
	next := 0
	for t := song[0].at; t <= end; t += ms {
		for ; next < len(song) && song[next].at <= t; next++ {
			out = append(out, r.Message(song[next].m, song[next].at)...)
		}
		if stalled == nil || !stalled(t) {
			out = append(out, r.Due(t)...)
		}
	}
	return out
}

// between counts the pulses sent after the first Start (or Continue) and before the Stop after it, the first pulse's time against
// Start's, and the pulses' times.
func between(sends []mydi.Send, from byte) (count int, startAt int64, times []int64) {
	started := false
	for _, s := range sends {
		switch {
		case s.Message[0] == from && !started:
			started, startAt = true, s.At
		case s.Message[0] == 0xFC && started:
			return count, startAt, times
		case s.Message[0] == 0xF8 && started:
			count++
			times = append(times, s.At)
		}
	}
	return count, startAt, times
}

func minSpacing(times []int64) int64 {
	least := int64(math.MaxInt64)
	for i := 1; i < len(times); i++ {
		least = min(least, times[i]-times[i-1])
	}
	return least
}

// spacingRMS is how far, root mean square, consecutive times are from this interval.
func spacingRMS(times []int64, interval float64) float64 {
	var sum float64
	for i := 1; i < len(times); i++ {
		d := float64(times[i]-times[i-1]) - interval
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(times)-1))
}

func remaker(offset time.Duration) *mydi.Remaker {
	return mydi.NewRemaker(mydi.NewFollower(mydi.DefaultFollow), mydi.RemakeConfig{Offset: offset})
}

func TestTheCountIsKeptFromStartToStopAndTheFirstPulseNeverPrecedesStart(t *testing.T) {
	for seed := int64(1); seed <= seeds(); seed++ {
		countCases(t, seed)
	}
}

func countCases(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	for _, c := range []struct {
		name   string
		jitter func(int) float64
	}{{"steady", nil}, {"jittered 2 ms RMS", gaussian(r, 2)}, {"a DAW's spikes", spikes(r)}} {
		s := newSource().play(24*40, 120, c.jitter)
		times := s.times
		re := remaker(0)
		// Stop in place of the source's next pulse, a little before it, after its last however late that came
		stop := max(s.next(120)-5*ms, times[len(times)-1]+ms)
		sends := play(re, transportTo(times, stop), times[len(times)-1]+int64(time.Second), nil)
		count, startAt, sent := between(sends, 0xFA)
		if count != len(times) {
			t.Fatalf("%s, seed %d: %d pulses sent between Start and Stop for %d received (%+v)", c.name, seed, count, len(times), re.Counts())
		}
		if sent[0] < startAt {
			t.Fatalf("%s, seed %d: the first pulse %d ns before Start", c.name, seed, startAt-sent[0])
		}
		if counts := re.Counts(); counts.Added != 0 || counts.Reported != 0 {
			t.Fatalf("%s, seed %d: %+v", c.name, seed, counts)
		}
	}
}

func TestAStopLaterThanTheNextPulseFindsOneSentAndItIsCounted(t *testing.T) {
	// the case no clock can know in advance: a Stop that comes after the source's next pulse would have, and its tolerance, so the line
	// sent that pulse; one pulse added, counted to be told, never hidden (ADR-0004 part D)
	s := newSource().play(24*8, 120, nil)
	re := remaker(0)
	sends := play(re, transportTo(s.times, s.next(120)+5*ms), s.next(120)+int64(time.Second), nil)
	if count, _, _ := between(sends, 0xFA); count != len(s.times)+1 || re.Counts().Added != 1 {
		t.Fatalf("%d sent for %d, %+v", count, len(s.times), re.Counts())
	}
}

func TestAStallIsCaughtUpNeverSkippedAndNeverCloserThanAQuarterInterval(t *testing.T) {
	times := newSource().play(24*40, 120, nil).times
	re := remaker(0)
	// the output stalled for 40 ms every two beats once locked
	stalled := func(now int64) bool { return now > times[48] && (now-times[0])%int64(time.Second) < 40*ms }
	sends := play(re, transport(times), times[len(times)-1]+int64(time.Second), stalled)
	count, _, sent := between(sends, 0xFA)
	quarter := int64(interval(120) / 4)
	if count != len(times) || re.Counts().CaughtUp == 0 {
		t.Fatalf("%d sent for %d, %+v", count, len(times), re.Counts())
	}
	if least := minSpacing(sent); least < quarter-ms/10 {
		t.Fatalf("two pulses %.2f ms apart, under a quarter interval", float64(least)/1e6)
	}
}

func TestTheOffsetIsBroughtInGraduallyAndKeepsTheCount(t *testing.T) {
	times := newSource().play(24*40, 120, nil).times
	re := remaker(10 * time.Millisecond)
	sends := play(re, transport(times), times[len(times)-1]+int64(time.Second), nil)
	count, startAt, sent := between(sends, 0xFA)
	if count != len(times) || sent[0] < startAt {
		t.Fatalf("%d sent for %d; first %d ns after Start", count, len(times), sent[0]-startAt)
	}
	// by the end, each pulse goes 10 ms before the source's: within the millisecond the test asks at
	early := float64(times[len(times)-1]-sent[len(sent)-1]) / 1e6
	if early < 9 || early > 11 {
		t.Fatalf("the last pulse %.2f ms early", early)
	}
	if least := minSpacing(sent); least < int64(interval(120)/4)-ms/10 {
		t.Fatalf("the slew put two pulses %.2f ms apart", float64(least)/1e6)
	}
}

func TestSongPositionOnlyWhileStoppedAndContinueStartsWithTheSourcesPulse(t *testing.T) {
	times := newSource().play(24*8, 120, nil).times
	re := remaker(0)
	song := transport(times[:96])
	song = append(song, heard{times[50], []byte{0xF2, 0x10, 0x00}}) // while running: dropped
	song = append(song, heard{times[96], []byte{0xF2, 0x20, 0x00}}) // stopped: sent on
	song = append(song, heard{times[97] - 5*ms, []byte{0xFB}})
	song = append(song, pulses(times[97:])...)
	song = append(song, heard{times[len(times)-1] + 5*ms, []byte{0xFC}})
	sends := play(re, song, times[len(times)-1]+int64(time.Second), nil)
	var positions int
	for _, s := range sends {
		if s.Message[0] == 0xF2 {
			positions++
		}
	}
	if positions != 1 || re.Counts().PositionsDropped != 1 {
		t.Fatalf("positions sent %d, %+v", positions, re.Counts())
	}
	count, continueAt, sent := between(sends, 0xFB)
	if count != len(times)-97 || sent[0] < continueAt {
		t.Fatalf("after Continue: %d sent for %d, first %d ns from Continue", count, len(times)-97, sent[0]-continueAt)
	}
}

func TestASourceThatClocksWhileStoppedKeepsThePortsClockedAndCountsFromStart(t *testing.T) {
	times := newSource().play(24*16, 120, nil).times
	re := remaker(0)
	song := pulses(times[:96]) // clock while stopped: the follower locks on it
	song = append(song, transport(times[96:])...)
	sends := play(re, song, times[len(times)-1]+int64(time.Second), nil)
	before := 0
	for _, s := range sends {
		if s.Message[0] == 0xFA {
			break
		}
		before++
	}
	count, startAt, sent := between(sends, 0xFA)
	if before != 96 || count != len(times)-96 || sent[0] < startAt {
		t.Fatalf("%d pulses relayed before Start (96 heard); %d after for %d", before, count, len(times)-96)
	}
}

func TestASourceLostIsHeldThenStoppedAndOneBackWithinTheHoldKeepsTheCount(t *testing.T) {
	// a cable pulled for ten pulses mid-song: the line's pulses go on, and when it is back the count is the source's
	s := newSource().play(24*8, 120, nil).rest(10, 120).play(24*8, 120, nil)
	re := remaker(0)
	sends := play(re, transport(s.times), s.times[len(s.times)-1]+int64(time.Second), nil)
	count, _, _ := between(sends, 0xFA)
	if want := len(s.times) + 10; count != want || re.Counts().Reported != 0 {
		t.Fatalf("%d sent for the source's %d, %+v", count, want, re.Counts())
	}
	// a source gone, no Stop: held eight beats, then Stop, once
	gone := newSource().play(24*8, 120, nil).times
	re = remaker(0)
	sends = play(re, transport(gone)[:len(gone)+1], gone[len(gone)-1]+int64(10*24*interval(120)), nil)
	stops := 0
	for _, s := range sends {
		if s.Message[0] == 0xFC {
			stops++
		}
	}
	if stops != 1 || re.Counts().StopsOnLoss != 1 {
		t.Fatalf("stops %d, %+v", stops, re.Counts())
	}
}

func TestAJitteryMastersClockIsSentOnSteadierThanItCame(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	times := newSource().play(24*40, 120, gaussian(r, 2)).times
	re := remaker(0)
	sends := play(re, transport(times), times[len(times)-1]+int64(time.Second), nil)
	_, _, sent := between(sends, 0xFA)
	// after the first beats (relayed while acquiring), against the source's own spacing over the same pulses
	in, out := spacingRMS(times[96:], interval(120)), spacingRMS(sent[96:], interval(120))
	if out >= in {
		t.Fatalf("spacing RMS out %.2f ms, in %.2f ms", out/1e6, in/1e6)
	}
	t.Logf("spacing RMS in %.2f ms, out %.2f ms (the test asks every millisecond)", in/1e6, out/1e6)
}

func TestAFloodNeverRunsTheOutputFasterThanAQuarterInterval(t *testing.T) {
	// F7: input faster than any clock (a pulse every half millisecond: a loop feeding itself, or a broken device), while stopped and
	// after Start: what goes out is never closer than a quarter interval, the uncounted relays dropped, the counted ones caught up
	for _, running := range []bool{false, true} {
		times := newSource().play(48, 120, nil).times
		flood := times[len(times)-1]
		for i := 0; i < 200; i++ {
			flood += ms / 2
			times = append(times, flood)
		}
		song := pulses(times)
		if running {
			song = transport(times)
		}
		re := remaker(0)
		sends := play(re, song, flood+int64(5*time.Second), nil)
		var sent []int64
		for _, s := range sends {
			if s.Message[0] == 0xF8 {
				sent = append(sent, s.At)
			}
		}
		if least := minSpacing(sent); least < 2*ms-ms/10 {
			t.Fatalf("running %v: two pulses %.2f ms apart", running, float64(least)/1e6)
		}
		if c := re.Counts(); !running && c.RelaysDropped == 0 {
			t.Fatalf("stopped: none dropped, %+v", c)
		}
	}
}

func TestAStartWithNoPulseSendsStartAloneAndWaits(t *testing.T) {
	// F4: Start, then no pulse at all: nothing goes out but Start, and the re-maker waits for the first pulse without losing a source it
	// never had
	re := remaker(0)
	start := int64(time.Second)
	sends := play(re, []heard{{start, []byte{0xFA}}}, start+int64(10*time.Second), nil)
	if len(sends) != 1 || sends[0].Message[0] != 0xFA || !re.Running() || re.Counts().StopsOnLoss != 0 {
		t.Fatalf("%v, running %v, %+v", sends, re.Running(), re.Counts())
	}
}

func TestLongStallsAreCaughtUpNeverSkipped(t *testing.T) {
	// F1: the output stalled 100 and 300 ms once locked, beyond the 40 ms the hub's own thread has shown: the count kept, the pulses
	// owed caught up no closer than a quarter interval
	for _, stall := range []int64{100 * ms, 300 * ms} {
		times := newSource().play(24*40, 120, nil).times
		re := remaker(0)
		stalled := func(now int64) bool { return now > times[96] && (now-times[0])%int64(4*time.Second) < stall }
		sends := play(re, transport(times), times[len(times)-1]+int64(time.Second), stalled)
		count, _, sent := between(sends, 0xFA)
		if count != len(times) || re.Counts().CaughtUp == 0 {
			t.Fatalf("a %d ms stall: %d sent for %d, %+v", stall/ms, count, len(times), re.Counts())
		}
		if least := minSpacing(sent); least < int64(interval(120)/4)-ms/10 {
			t.Fatalf("a %d ms stall: two pulses %.2f ms apart", stall/ms, float64(least)/1e6)
		}
	}
}
