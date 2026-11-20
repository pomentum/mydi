package mydi_test

import (
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// A generated source (ADR-0004 part G step 2): its pulses' times in nanoseconds, from one second on, at a tempo that may move, each
// pulse moved by a jitter of its own.
type source struct {
	times []int64
	at    float64
}

func newSource() *source { return &source{at: float64(time.Second)} }

func interval(bpm float64) float64 { return float64(time.Minute) / (bpm * 24) }

// play adds n pulses at this tempo, each moved by jitter(i) nanoseconds.
func (s *source) play(n int, bpm float64, jitter func(int) float64) *source {
	for i := 0; i < n; i++ {
		s.at += interval(bpm)
		j := 0.0
		if jitter != nil {
			j = jitter(len(s.times))
		}
		t := int64(s.at + j)
		// a source sends its pulses in their order: a pulse moved early still comes after the one before it
		if n := len(s.times); n > 0 && t <= s.times[n-1] {
			t = s.times[n-1] + int64(500*time.Microsecond)
		}
		s.times = append(s.times, t)
	}
	return s
}

// ramp adds n pulses whose tempo moves evenly from one to the other.
func (s *source) ramp(n int, from, to float64) *source {
	for i := 0; i < n; i++ {
		s.at += interval(from + (to-from)*float64(i)/float64(n))
		s.times = append(s.times, int64(s.at))
	}
	return s
}

// uneven adds n pulses in a device's own uneven pattern: the spacings a synth sent at a displayed 24.8 BPM over USB (2026-11-15),
// four short and one double, its tempo right on average.
func (s *source) uneven(n int) *source {
	pattern := []float64{83, 84, 83, 83, 167}
	for i := 0; i < n; i++ {
		s.at += pattern[i%len(pattern)] * float64(time.Millisecond)
		s.times = append(s.times, int64(s.at))
	}
	return s
}

// next is where the source's next pulse would be, at this tempo: where a source's Stop comes in its place.
func (s *source) next(bpm float64) int64 { return int64(s.at + interval(bpm)) }

// rest leaves this many pulses out, as a source that drops out does.
func (s *source) rest(n int, bpm float64) *source {
	s.at += float64(n) * interval(bpm)
	return s
}

// follow feeds the follower every pulse, looking at the time just before each, as a program's clock would; it answers the pulse number
// at which the follower first locked (-1: never) and the states it went through.
func follow(f *mydi.Follower, times []int64) (lockedAt int, states []string) {
	lockedAt = -1
	last := ""
	for i, t := range times {
		for look := t - int64(5*time.Millisecond); look < t; look += int64(time.Millisecond) {
			f.Check(look)
		}
		if f.State() != last {
			states, last = append(states, f.State()), f.State()
		}
		f.Pulse(t)
		if f.State() != last {
			states, last = append(states, f.State()), f.State()
		}
		if lockedAt < 0 && f.State() == mydi.FollowLocked {
			lockedAt = i + 1
		}
	}
	return lockedAt, states
}

func uniform(r *rand.Rand, ms float64) func(int) float64 {
	return func(int) float64 { return (r.Float64()*2 - 1) * ms * 1e6 }
}

func gaussian(r *rand.Rand, ms float64) func(int) float64 {
	return func(int) float64 { return r.NormFloat64() * ms * 1e6 }
}

// spikes is a DAW's profile as one maker measured it: mostly half a millisecond, one pulse in twenty moved -38 to +22 ms.
func spikes(r *rand.Rand) func(int) float64 {
	return func(int) float64 {
		if r.Intn(20) == 0 {
			return (-38 + r.Float64()*60) * 1e6
		}
		return r.NormFloat64() * 0.5e6
	}
}

func TestTheFollowerLocksOnASourceAndKnowsItsTempo(t *testing.T) {
	for seed := int64(1); seed <= seeds(); seed++ {
		followCases(t, seed)
	}
}

// seeds is how many seeds each jittered source is drawn with: twenty, so a pass is not one lucky draw; MYDI_FOLLOW_SEEDS asks for more.
func seeds() int64 {
	if n, err := strconv.ParseInt(os.Getenv("MYDI_FOLLOW_SEEDS"), 10, 64); err == nil && n > 0 {
		return n
	}
	return 20
}

func followCases(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	for _, c := range []struct {
		name       string
		bpm        float64
		jitter     func(int) float64
		lockWithin int     // pulses: part G's time to lock, in beats × 24
		tempoErr   float64 // BPM: part G's steady tempo error
	}{
		{"steady at 120", 120, nil, 48, 0.1},
		{"steady at 30", 30, nil, 48, 0.1},
		{"steady at 300", 300, nil, 48, 0.1},
		{"jittered ±2 ms", 120, uniform(r, 2), 96, 0.2},
		{"jittered 2 ms RMS, a DAW on Windows", 120, gaussian(r, 2), 96, 0.2},
		{"a DAW's spikes, -38 to +22 ms", 120, spikes(r), 96, 0.2},
		{"drifting +0.5%", 120.6, uniform(r, 0.5), 96, 0.1},
	} {
		f := mydi.NewFollower(mydi.DefaultFollow)
		lockedAt, states := follow(f, newSource().play(24*16, c.bpm, c.jitter).times)
		if lockedAt < 0 || lockedAt > c.lockWithin {
			t.Fatalf("%s, seed %d: locked at pulse %d (states %v), wanted within %d", c.name, seed, lockedAt, states, c.lockWithin)
		}
		if err := math.Abs(f.Tempo() - c.bpm); err > c.tempoErr {
			t.Fatalf("%s, seed %d: tempo %.3f, %.3f off", c.name, seed, f.Tempo(), err)
		}
		if counts := f.Counts(); counts.Jumps != 0 || counts.Losses != 0 {
			t.Fatalf("%s, seed %d: %+v", c.name, seed, counts)
		}
		if seed > 1 {
			continue
		}
		t.Logf("%s: locked at pulse %d, tempo %.3f, spread %.2f ms, outliers %d", c.name, lockedAt, f.Tempo(), f.Spread()/1e6, f.Counts().Outliers)
	}
}

func TestARampIsFollowedAndNeverTakenForAJump(t *testing.T) {
	for _, c := range []struct{ from, to float64 }{{120, 140}, {140, 100}} {
		f := mydi.NewFollower(mydi.DefaultFollow)
		follow(f, newSource().play(96, c.from, nil).ramp(24*4*8, c.from, c.to).play(96, c.to, nil).times)
		if counts := f.Counts(); counts.Jumps != 0 || f.State() != mydi.FollowLocked {
			t.Fatalf("%v→%v: jumps %d, %s", c.from, c.to, counts.Jumps, f.State())
		}
		if err := math.Abs(f.Tempo() - c.to); err > 0.1 {
			t.Fatalf("%v→%v: ended at %.3f", c.from, c.to, f.Tempo())
		}
	}
}

func TestAJumpIsReacquiredAtTheNewTempo(t *testing.T) {
	for _, to := range []float64{160, 60, 240} { // past 25%, half and double time
		f := mydi.NewFollower(mydi.DefaultFollow)
		s := newSource().play(24*8, 120, nil)
		jumpAt := len(s.times)
		follow(f, s.play(24*8, to, nil).times)
		if counts := f.Counts(); counts.Jumps != 1 || f.State() != mydi.FollowLocked {
			t.Fatalf("to %v: %+v, %s", to, counts, f.State())
		}
		if err := math.Abs(f.Tempo() - to); err > 0.1 {
			t.Fatalf("to %v: tempo %.3f", to, f.Tempo())
		}
		// locked again within two beats of the jump: replayed to find the pulse
		g := mydi.NewFollower(mydi.DefaultFollow)
		follow(g, s.times[:jumpAt])
		relocked := -1
		for i, at := range s.times[jumpAt:] {
			g.Pulse(at)
			if g.Counts().Jumps == 1 && g.State() == mydi.FollowLocked && math.Abs(g.Tempo()-to) < 1 {
				relocked = i + 1
				break
			}
		}
		if relocked < 0 || relocked > 48 {
			t.Fatalf("to %v: relocked after %d pulses", to, relocked)
		}
	}
}

func TestDropoutsAreBridgedOrHeldAndASourceBackIsTakenAgain(t *testing.T) {
	// three pulses missing: under the loss limit (the larger of four intervals and 120 ms), bridged, the count kept by the line
	f := mydi.NewFollower(mydi.DefaultFollow)
	follow(f, newSource().play(96, 120, nil).rest(3, 120).play(96, 120, nil).times)
	if c := f.Counts(); c.Losses != 0 || c.Missing != 3 || f.State() != mydi.FollowLocked {
		t.Fatalf("three missing: %+v, %s", c, f.State())
	}
	// ten missing: past the limit, held, and taken again when it is back within the hold
	g := mydi.NewFollower(mydi.DefaultFollow)
	_, states := follow(g, newSource().play(96, 120, nil).rest(10, 120).play(96, 120, nil).times)
	if c := g.Counts(); c.Holds != 1 || c.Reacquired != 1 || c.Missing != 10 || g.State() != mydi.FollowLocked {
		t.Fatalf("ten missing: %+v, %s, %v", c, g.State(), states)
	}
	// silent past the hold (eight beats): lost; a source back after it is acquired again from nothing
	h := mydi.NewFollower(mydi.DefaultFollow)
	s := newSource().play(96, 120, nil)
	follow(h, s.times)
	h.Check(s.times[len(s.times)-1] + int64(9*24*interval(120)))
	if h.State() != mydi.FollowLost {
		t.Fatalf("past the hold: %s", h.State())
	}
	follow(h, s.rest(24*9, 120).play(48, 90, nil).times[96:])
	if h.State() != mydi.FollowLocked || math.Abs(h.Tempo()-90) > 0.1 {
		t.Fatalf("back after the hold: %s at %.3f", h.State(), h.Tempo())
	}
}

func TestBunchedInputIsSetAsideNotLearned(t *testing.T) {
	// the reader held now and then: a pulse 15 ms late and the next on time, as a stamp taken on delivery would show it
	late := func(i int) float64 {
		if i%24 == 23 {
			return 15e6
		}
		return 0
	}
	f := mydi.NewFollower(mydi.DefaultFollow)
	follow(f, newSource().play(24*16, 120, late).times)
	if c := f.Counts(); c.Jumps != 0 || c.Outliers == 0 || math.Abs(f.Tempo()-120) > 0.1 {
		t.Fatalf("%+v at %.3f", c, f.Tempo())
	}
}
