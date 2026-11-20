package mydi_test

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// A source that changes tempo while it plays: a slow ramp over bars, a knob turned in a beat, a jump, and a tempo that wobbles a few
// percent for a second and comes back. Each design's pulses are measured against the source's own times of the same place once locked
// (its true times, before the jitter and the system's millisecond stamps the hub sees), and its spacing against the source's true
// spacing: phase says how late the music is, spacing how steady.

type rampCase struct {
	name string
	song func(s *source) *source
	// the line's bounds, the phase's 99th percentile and largest in milliseconds: what it measured (2026-11-15) with a margin, so a
	// change that makes it worse fails; the fastest tempo the case reaches, for its interval
	p99, most, fastest float64
}

var rampCases = []rampCase{
	{"steady at 120 BPM", func(s *source) *source { return s.play(24*16, 120, nil) }, 0.5, 1, 120},
	// a long ramp: the line lags it by half its window, so a drift of the residuals to one side over a beat is moving too
	{"120 to 140 BPM over eight bars", func(s *source) *source { return s.ramp(24*4*8, 120, 140).play(96, 140, nil) }, 2, 3, 140},
	{"120 to 100 BPM over eight bars", func(s *source) *source { return s.ramp(24*4*8, 120, 100).play(96, 100, nil) }, 2, 3, 120},
	{"120 to 130 BPM in a beat", func(s *source) *source { return s.ramp(24, 120, 130).play(24*8, 130, nil) }, 2.5, 4.5, 130},
	{"120 to 100 BPM in a beat", func(s *source) *source { return s.ramp(24, 120, 100).play(24*8, 100, nil) }, 3, 7, 120},
	{"5% up and back over two beats", func(s *source) *source {
		return s.ramp(24, 120, 126).ramp(24, 126, 120).play(24*8, 120, nil)
	}, 3, 6, 126},
	{"a jump from 120 to 140 BPM", func(s *source) *source { return s.play(24*8, 140, nil) }, 1.5, 4, 140},
	// a synth's own clock under 30 BPM, uneven: four pulses 83 ms apart and one 167 ms; too uneven to re-make, its own pulses are sent on
	{"a device's uneven clock at 25 BPM", func(s *source) *source { return s.ramp(24*4, 120, 25).uneven(24 * 8) }, 4, 9, 25},
	{"a jump from 120 to 90 BPM", func(s *source) *source { return s.play(24*8, 90, nil) }, 1.5, 7, 120},
}

// designs: the pulses sent on for a song, by each way of sending a clock on.
var designs = map[string]func(song []heard, end int64, r *rand.Rand) []mydi.Send{
	// each pulse sent on as it came: no estimate, the source's own jitter passed on
	"relay": func(song []heard, end int64, r *rand.Rand) []mydi.Send {
		var out []mydi.Send
		for _, h := range song {
			out = append(out, mydi.Send{At: h.at, Message: h.m})
		}
		return out
	},
	"line, loop guard on": func(song []heard, end int64, r *rand.Rand) []mydi.Send {
		room := &loopback{r: r, echoFrom: math.MaxInt64}
		return room.run(withEcho(0), song, nil, end)
	},
	"line": func(song []heard, end int64, r *rand.Rand) []mydi.Send {
		room := &loopback{r: r, echoFrom: math.MaxInt64}
		return room.run(mydi.NewRemaker(mydi.NewFollower(mydi.DefaultFollow), mydi.RemakeConfig{}), song, nil, end)
	},
}

// designResult is one design on one case: the count kept or not, each pulse's phase against the source's true time from the fourth beat,
// and its spacing's difference from the source's true spacing.
func designResult(design string, c rampCase, seed int64, jitter float64) (kept bool, phase, spacing []float64) {
	r := rand.New(rand.NewSource(seed))
	s := c.song(newSource().play(24*4, 120, nil))
	stamped := make([]int64, len(s.times))
	for i, t := range s.times {
		t += int64(r.NormFloat64() * jitter * 1e6)
		if i > 0 && t <= stamped[i-1] {
			t = stamped[i-1] + 500*int64(time.Microsecond)
		}
		stamped[i] = int64(math.Floor(float64(t)/float64(ms)))*ms + ms/2
	}
	sends := designs[design](transport(stamped), stamped[len(stamped)-1]+int64(time.Second), r)
	count, _, sent := between(sends, 0xFA)
	for i := 96; i < min(len(sent), len(s.times)); i++ {
		phase = append(phase, float64(sent[i]-s.times[i]))
		spacing = append(spacing, float64((sent[i]-sent[i-1])-(s.times[i]-s.times[i-1])))
	}
	return count == len(s.times), phase, spacing
}

func percentiles(values []float64) (median, p99, most float64) {
	abs := make([]float64, len(values))
	for i, v := range values {
		abs[i] = math.Abs(v)
	}
	sort.Float64s(abs)
	return abs[len(abs)/2], abs[len(abs)*99/100], abs[len(abs)-1]
}

func rms(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(values)))
}

func TestATempoChangeIsFollowedAndComparedWithARelay(t *testing.T) {
	for _, c := range rampCases {
		for _, design := range []string{"relay", "line", "line, loop guard on"} {
			var phase, spacing []float64
			lost := 0
			for seed := int64(1); seed <= max(seeds(), 10); seed++ {
				kept, p, s := designResult(design, c, seed, 0.3)
				if !kept {
					lost++
				}
				phase, spacing = append(phase, p...), append(spacing, s...)
			}
			median, p99, most := percentiles(phase)
			quarter := interval(c.fastest) / 4
			pastQuarter := 0
			for _, p := range phase {
				if math.Abs(p) > quarter {
					pastQuarter++
				}
			}
			t.Logf("%-32s %-20s count lost in %2d seeds; phase median %5.2f, p99 %6.2f, at most %7.2f ms; spacing RMS %.2f ms; %d past a quarter interval",
				c.name, design, lost, median/1e6, p99/1e6, most/1e6, rms(spacing)/1e6, pastQuarter)
			if design == "relay" {
				continue
			}
			// the line, by default and with the loop guard on: the count kept, and each pulse sent standing for the source's of its place,
			// nearer it than either neighbour (within half an interval); by default within the bounds it measured too (the guard costs an
			// uneven source its quality, logged above)
			switch {
			case lost > 0:
				t.Errorf("%s, %s: the count lost in %d seeds", c.name, design, lost)
			case most > interval(c.fastest)/2:
				t.Errorf("%s, %s: a pulse %.2f ms off the source's of its place, past half an interval", c.name, design, most/1e6)
			case design == "line" && (p99 > c.p99*1e6 || most > c.most*1e6):
				t.Errorf("%s, %s: phase p99 %.2f and at most %.2f ms, past its bounds %.1f and %.1f", c.name, design, p99/1e6, most/1e6, c.p99, c.most)
			}
		}
	}
}
