package mydi_test

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// loopback is the room the hub plays into: each pulse it writes is written a little off its time (the scheduler's own irregularity),
// told to the re-maker as written, and, from echoFrom on, comes back into the port followed after a delay, as a synth's soft thru or a
// chain closed round sends it.
type loopback struct {
	r        *rand.Rand
	echoFrom int64
	delay    time.Duration
	pending  []heard
}

func (l *loopback) push(h heard) {
	i := sort.Search(len(l.pending), func(i int) bool { return l.pending[i].at > h.at })
	l.pending = append(l.pending, heard{})
	copy(l.pending[i+1:], l.pending[i:])
	l.pending[i] = h
}

// wrote is a pulse the hub wrote: told to the re-maker, and echoed when the loop is closed.
func (l *loopback) wrote(re *mydi.Remaker, at int64) {
	actual := at + int64(l.r.NormFloat64()*0.2e6)
	re.Wrote(actual)
	if actual >= l.echoFrom {
		l.push(heard{actual + int64(l.delay) + int64(l.r.NormFloat64()*0.02e6), []byte{0xF8}})
	}
}

// run plays the source and the hub's own clock (its writes, when it clocks the room itself) through the room, asking the re-maker what
// is due every millisecond, and answers what it sent.
func (l *loopback) run(re *mydi.Remaker, song []heard, ownClock []int64, end int64) []mydi.Send {
	for _, h := range song {
		l.push(h)
	}
	var out []mydi.Send
	own := 0
	start := int64(math.MaxInt64)
	if len(l.pending) > 0 {
		start = l.pending[0].at
	}
	if len(ownClock) > 0 {
		start = min(start, ownClock[0])
	}
	for t := start; t <= end; t += ms {
		for ; own < len(ownClock) && ownClock[own] <= t; own++ {
			l.wrote(re, ownClock[own])
		}
		var sends []mydi.Send
		for len(l.pending) > 0 && l.pending[0].at <= t {
			h := l.pending[0]
			l.pending = l.pending[1:]
			sends = append(sends, re.Message(h.m, h.at)...)
		}
		sends = append(sends, re.Due(t)...)
		for _, s := range sends {
			if s.Message[0] == 0xF8 {
				l.wrote(re, s.At)
			}
		}
		out = append(out, sends...)
	}
	return out
}

func withEcho(dither time.Duration) *mydi.Remaker {
	c := mydi.DefaultEcho
	return mydi.NewRemaker(mydi.NewFollower(mydi.DefaultFollow), mydi.RemakeConfig{Echo: &c, Dither: dither})
}

func TestTheSpurNeverFollowsItsOwnClockComingBack(t *testing.T) {
	// the hub clocks a synth at 125 BPM; the synth's soft thru brings it back into the port a listener asks to follow; no master
	r := rand.New(rand.NewSource(11))
	var ownClock []int64
	for k := 0; k < 24*32; k++ {
		ownClock = append(ownClock, int64(float64(time.Second)+float64(k)*interval(125)))
	}
	re := withEcho(0)
	room := &loopback{r: r, echoFrom: 0, delay: 3 * time.Millisecond}
	sends := room.run(re, nil, ownClock, ownClock[len(ownClock)-1]+int64(time.Second))
	f := re.Counts()
	if len(sends) != 0 || f.Echoes == 0 {
		t.Fatalf("%d sent on, %+v", len(sends), f)
	}
	t.Logf("its own clock come back: %d refused, %d held a moment, nothing sent on, never locked", f.Echoes, f.Suspects)
}

func TestAMasterFollowedThroughAChainThatEchoesKeepsItsCount(t *testing.T) {
	// following a master; after eight bars a chain closed round brings the hub's own pulses back 4 ms after it writes them. Part G asks
	// for the count kept in every case: one seed that misses it fails the test, each miss named. At least 300 seeds, since a miss was once
	// as rare as one in a few hundred (ADR-0004, step 2's echo note).
	n := max(seeds(), 300)
	var misses []string
	for seed := int64(1); seed <= n; seed++ {
		r := rand.New(rand.NewSource(seed))
		s := newSource().play(24*40, 120, gaussian(r, 0.5))
		re := withEcho(0)
		room := &loopback{r: r, echoFrom: s.times[24*8], delay: 4 * time.Millisecond}
		stop := max(s.next(120)-5*ms, s.times[len(s.times)-1]+ms)
		sends := room.run(re, transportTo(s.times, stop), nil, s.times[len(s.times)-1]+int64(time.Second))
		count, startAt, sent := between(sends, 0xFA)
		c := re.Counts()
		if sent[0] < startAt || c.Echoes < int64(24*30) {
			t.Fatalf("seed %d: the first pulse before Start, or the echoes not refused: %+v", seed, c)
		}
		if count != len(s.times) {
			misses = append(misses, fmt.Sprintf("seed %d: %d sent for %d", seed, count, len(s.times)))
		}
		if seed == 1 {
			t.Logf("a chain closed after eight bars: %d pulses sent for %d, %d echoes refused, %d held a moment", count, len(s.times), c.Echoes, c.Suspects)
		}
	}
	if len(misses) > 0 {
		t.Fatalf("the count missed in %d of %d seeds: %v", len(misses), n, misses)
	}
}

func TestAMasterSilentWhileStoppedIsSentOnFromItsFirstBeatWithTheEchoTestOn(t *testing.T) {
	// a master that sends no clock while stopped (most DAWs), followed with the echo test on and no loop: each pulse after the first pairs
	// with the hub's relay of the one before, at the source's own interval, so it has no evidence either way. Held until the test had
	// some, it starved the test of the very writes it needed, and the first beat went out up to a third of a second late, decided by the
	// time-out (2026-11-15). An even run of four pulses is the master's (an echo is an extra inside an interval), and a pulse jittered off
	// the run is counted in. The bounds are the design's, not fitted: a clean source's second pulse waits for its fourth, two intervals;
	// a jittered source's held pulses are decided within the echo test's own evidence, eight spacings, never by the time-out.
	for _, c := range []struct {
		jitter float64
		first  float64 // intervals: the latest a pulse of the first beat may go out
	}{{0, 2}, {0.5, float64(mydi.DefaultEcho.Evidence)}} {
		for _, bpm := range []float64{120, 240} {
			var worst []float64
			worstAfter := 0.0
			for seed := int64(1); seed <= max(seeds(), 50); seed++ {
				r := rand.New(rand.NewSource(seed))
				s := newSource().play(24*8, bpm, gaussian(r, c.jitter))
				re := withEcho(0)
				room := &loopback{r: r, echoFrom: math.MaxInt64}
				sends := room.run(re, transportTo(s.times, s.next(bpm)-5*ms), nil, s.times[len(s.times)-1]+int64(time.Second))
				count, startAt, sent := between(sends, 0xFA)
				if count != len(s.times) || sent[0] < startAt {
					t.Fatalf("%v BPM, seed %d: %d sent for %d, the first %d ns after Start", bpm, seed, count, len(s.times), sent[0]-startAt)
				}
				first := 0.0
				for i := 1; i < len(sent); i++ {
					late := float64(sent[i]-s.times[i]) / interval(bpm)
					if i < 24 {
						first = math.Max(first, late)
					} else {
						worstAfter = math.Max(worstAfter, late)
					}
				}
				worst = append(worst, first)
			}
			sort.Float64s(worst)
			p99, most := worst[len(worst)*99/100], worst[len(worst)-1]
			if most > c.first+0.05 || worstAfter > 0.5 {
				t.Fatalf("jitter %v ms, %v BPM: a pulse of the first beat %.2f intervals late (at most %.0f), one after it %.2f", c.jitter, bpm, most, c.first, worstAfter)
			}
			t.Logf("jitter %v ms, %v BPM: the first beat's latest pulse, p99 %.2f and at most %.2f intervals (%.1f ms); after it at most %.2f",
				c.jitter, bpm, p99, most, most*interval(bpm)/1e6, worstAfter)
		}
	}
}
