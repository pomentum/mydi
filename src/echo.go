package src

import (
	"math"
	"sort"
	"time"
)

// The echo test (ADR-0004 part A): input that is the hub's own clock coming back (a synth's soft thru, a DIN chain looped round) is
// known by the hub's own timing fingerprint. The pulses the hub writes are never perfectly spaced: each is a little off its grid, pulse
// by pulse. An echo carries those irregularities back at a fixed delay; a real master's are its own. So for each delay up to MaxDelay at
// which recent input pulses sit after pulses the hub wrote, the input's spacing irregularities are correlated with those of the pulses
// they pair with: an echo correlates near one, a master near zero or below, even when the hub follows it and its pulses sit at a near
// fixed delay after the master's. The times compared are those the hub wrote at and the stamps its callback took, never the times it
// aimed for, which lie on a smooth line and carry no fingerprint. Where the fingerprint is too faint to tell (a hub whose writes are
// too regular, or input stamps rounded to USB's millisecond frames), a deliberate dither on the hub's own output is the fallback.

// EchoConfig holds the echo test's constants (part G: set by the soft-thru and DIN-chain cases).
type EchoConfig struct {
	// An echo comes back MinDelay to MaxDelay after the pulse it echoes. To decide, input pulses are paired with the hub's within Pair of
	// a delay, loosely, so the pairs are not chosen for agreeing, which would make any source correlate; once the delay is known, each
	// pulse within Match of it is refused.
	MinDelay, MaxDelay, Pair, Match time.Duration
	// Window pairs decide; Threshold is the correlation above which they are an echo.
	Window    int
	Threshold float64
	// MinFingerprint is the least spread of the hub's own spacing irregularities that tells an echo from a master.
	MinFingerprint time.Duration
	// Coverage is the least share of the hub's pulses across the span that an echo's pairs take.
	Coverage float64
	// Evidence is how many spacings a delay needs before its correlation counts against an echo: fewer are too few to judge either way.
	Evidence int
}

// DefaultEcho is part G's provisional echo constants.
var DefaultEcho = EchoConfig{
	MinDelay: 300 * time.Microsecond, MaxDelay: 50 * time.Millisecond, Pair: 2 * time.Millisecond, Match: 600 * time.Microsecond,
	Window: 48, Threshold: 0.7, MinFingerprint: 50 * time.Microsecond, Coverage: 0.8, Evidence: 8,
}

// What the echo test says of one input pulse.
const (
	EchoNone    = "real"    // not the hub's own: no pulse of the hub's it could echo, or every delay it could echo at refused
	EchoYes     = "echo"    // the hub's own, at the delay established
	EchoSuspect = "suspect" // too few pulses yet at some delay to say, and those there are look like an echo
	EchoUnknown = "unknown" // it pairs with a pulse the hub wrote, and there is no evidence yet either way
)

// Echo tells the hub's own clock coming back from a real master.
type Echo struct {
	c      EchoConfig
	wrote  []int64 // what the hub wrote, oldest first
	heard  []int64 // what came in, oldest first
	delay  int64   // the echo's delay once established, nanoseconds; 0: none
	faint  uint64  // decisions made with too faint a fingerprint
	lastCo float64 // the last correlation found
}

// NewEcho is an echo test with these constants.
func NewEcho(c EchoConfig) *Echo { return &Echo{c: c} }

// Wrote records a pulse the hub wrote, at the time it wrote it, in its time order among the others: a pulse scheduled ahead (caught up
// after a stall) may be told before one written earlier.
func (e *Echo) Wrote(at int64) {
	i := sort.Search(len(e.wrote), func(i int) bool { return e.wrote[i] > at })
	e.wrote = append(e.wrote, 0)
	copy(e.wrote[i+1:], e.wrote[i:])
	e.wrote[i] = at
	if len(e.wrote) > 1024 {
		e.wrote = e.wrote[len(e.wrote)-512:]
	}
}

// Delay is the echo's delay once established, and whether it is.
func (e *Echo) Delay() (time.Duration, bool) { return time.Duration(e.delay), e.delay != 0 }

// Faint counts the decisions made on a fingerprint too faint to trust: the fallback, a dither, is then due.
func (e *Echo) Faint() uint64 { return e.faint }

// Correlation is the last correlation the test found, for the counts.
func (e *Echo) Correlation() float64 { return e.lastCo }

// nearest is the pulse the hub wrote nearest this time, and how far from it.
func (e *Echo) nearest(at int64) (int, int64) {
	i := sort.Search(len(e.wrote), func(i int) bool { return e.wrote[i] >= at })
	best, dist := -1, int64(math.MaxInt64)
	for _, j := range []int{i - 1, i} {
		if j >= 0 && j < len(e.wrote) {
			if d := abs64(e.wrote[j] - at); d < dist {
				best, dist = j, d
			}
		}
	}
	return best, dist
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Judge says of one input pulse, heard at this time, whether it is the hub's own coming back.
func (e *Echo) Judge(at int64) string {
	e.heard = append(e.heard, at)
	if len(e.heard) > 256 {
		e.heard = e.heard[len(e.heard)-128:]
	}
	return e.judge(at)
}

// Resolve judges again a pulse heard earlier and held as suspect, on what is known now.
func (e *Echo) Resolve(at int64) string { return e.judge(at) }

func (e *Echo) judge(at int64) string {
	match := int64(e.c.Match)
	// an established delay is checked again every beat: one whose evidence no longer holds is dropped, so a chance decision does not
	// refuse a master for good
	if e.delay != 0 && len(e.heard)%24 == 0 {
		if co, pairs, _, _, delaySpread, coverage := e.correlate(e.delay); pairs >= e.c.Window &&
			!(co >= e.c.Threshold && delaySpread <= float64(e.c.Match) && coverage >= e.c.Coverage) {
			e.delay = 0
		}
	}
	if e.delay != 0 {
		if j, dist := e.nearest(at - e.delay); dist <= match && at-e.wrote[j] >= int64(e.c.MinDelay) {
			return EchoYes
		}
	}
	// the delays it could be an echo at: after a pulse the hub wrote, within the window
	var delays []int64
	for i := len(e.wrote) - 1; i >= 0; i-- {
		d := at - e.wrote[i]
		if d > int64(e.c.MaxDelay) {
			break
		}
		if d >= int64(e.c.MinDelay) {
			delays = append(delays, d)
		}
	}
	if len(delays) == 0 {
		return EchoNone
	}
	verdict := EchoNone
	for _, d := range delays {
		co, pairs, spacings, fingerprint, delaySpread, coverage := e.correlate(d)
		looksLike := co >= e.c.Threshold && delaySpread <= float64(e.c.Match) && coverage >= e.c.Coverage
		if pairs < e.c.Window {
			// too few to decide: suspect when what there is already looks like an echo; unknown while there are too few spacings to judge
			// either way, so the first pulses of an echo are not relayed back out; a master's pulses, with evidence against, neither
			switch {
			case looksLike:
				verdict = EchoSuspect
			case spacings < e.c.Evidence && verdict == EchoNone:
				verdict = EchoUnknown
			}
			continue
		}
		e.lastCo = co
		if fingerprint < float64(e.c.MinFingerprint) {
			// too faint to tell: taken as real, and counted, so the dither is turned on
			e.faint++
			continue
		}
		// both: the hub's irregularities carried, and one delay held to within the match; a master's pulses near the hub's, phase
		// locked, spread with the master's own jitter
		if looksLike {
			e.delay = d
			return EchoYes
		}
	}
	return verdict
}

// correlate pairs the recent input pulses that sit this delay after a pulse the hub wrote, and measures the trailing run of those pairs:
// going back from the newest, one pair after another, until a gap of more than two of the hub's pulses. An echo is such a run, a pair for
// each pulse the hub wrote; chance pairs long before it are not part of it. Over the run it answers the correlation of the spacing
// irregularities of consecutive pairs, how many pairs and spacings, the spread of the hub's own spacing irregularities among them, how
// far the pairs' delays spread (an echo's within the stamps' own noise of one delay), and what share of the hub's pulses across the run
// the pairs take (an echo nearly all; a delay at the edge of a master's spread, a share).
func (e *Echo) correlate(delay int64) (co float64, pairs, spacings int, fingerprint, delaySpread, coverage float64) {
	match := int64(e.c.Pair)
	type pair struct {
		t int64
		j int
	}
	var all []pair
	for _, t := range e.heard {
		j, dist := e.nearest(t - delay)
		// an echo comes after the pulse it echoes: a master's pulse that comes just before the hub's write, which follows it, is no pair
		if j < 0 || dist > match || t-e.wrote[j] < int64(e.c.MinDelay) {
			continue
		}
		// a write is echoed once: when two input pulses pair with it, the one nearer the delay is the echo, the other a master's by chance
		if n := len(all); n > 0 && all[n-1].j == j {
			if abs64(t-e.wrote[j]-delay) < abs64(all[n-1].t-e.wrote[j]-delay) {
				all[n-1] = pair{t, j}
			}
			continue
		}
		all = append(all, pair{t, j})
	}
	if len(all) == 0 {
		return 0, 0, 0, 0, math.Inf(1), 0
	}
	first := len(all) - 1
	for first > 0 && all[first-1].j >= all[first].j-2 {
		first--
	}
	run := all[first:]
	var in, out, delays []float64
	for i, p := range run {
		delays = append(delays, float64(p.t-e.wrote[p.j]))
		// a pulse that pairs with none is skipped, not a break: a master's pulses between a chain's echoes leave the echoes' run whole
		if i > 0 && p.j == run[i-1].j+1 {
			in = append(in, float64(p.t-run[i-1].t))
			out = append(out, float64(e.wrote[p.j]-e.wrote[run[i-1].j]))
		}
	}
	pairs = len(run)
	coverage = float64(len(run)) / float64(run[len(run)-1].j-run[0].j+1)
	// single steps alone: a gap (pulses held or missing on both sides) is a large spacing on both, and a few of those would make any two
	// series correlate; the fingerprint is in the small irregularities of single steps
	if len(in) >= 3 {
		sorted := append([]float64(nil), in...)
		sort.Float64s(sorted)
		step := sorted[len(sorted)/2]
		kept := 0
		for i := range in {
			if math.Abs(in[i]-step) <= step/2 && math.Abs(out[i]-step) <= step/2 {
				in[kept], out[kept] = in[i], out[i]
				kept++
			}
		}
		in, out = in[:kept], out[:kept]
	}
	if len(in) < 3 {
		return 0, pairs, len(in), 0, math.Inf(1), coverage
	}
	co, fingerprint = pearson(in, out)
	_, delaySpread = pearson(delays, delays)
	return co, pairs, len(in), fingerprint, delaySpread, coverage
}

// pearson is the correlation of two series, and the second's standard deviation.
func pearson(a, b []float64) (float64, float64) {
	n := float64(len(a))
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma, mb = ma/n, mb/n
	var sab, saa, sbb float64
	for i := range a {
		da, db := a[i]-ma, b[i]-mb
		sab += da * db
		saa += da * da
		sbb += db * db
	}
	if saa == 0 || sbb == 0 {
		return 0, math.Sqrt(sbb / n)
	}
	return sab / math.Sqrt(saa*sbb), math.Sqrt(sbb / n)
}

// Dither is the fallback signature (part A): a deliberate offset for the hub's i-th written pulse, within ±amplitude, from a fixed
// pseudo-random sequence, so the hub's own output carries a pattern an echo cannot lose in a millisecond's rounding.
func Dither(i uint64, amplitude time.Duration) int64 {
	x := i*0x9E3779B97F4A7C15 + 0x632BE59BD9B4E019
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 29
	return int64((float64(x%2001)/1000 - 1) * float64(amplitude))
}
