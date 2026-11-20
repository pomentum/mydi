package mydi

import (
	"math"
	"sort"
	"time"
)

// The follower (ADR-0004 part C): a device's MIDI clock, its pulses' times in, its tempo and phase out, as plain code with no port and
// no thread, so every case of part G is a test. A least-squares line through the times of the source's recent pulses, indexed by their
// place in the source's count: its slope the interval, its intercept the phase. Outliers set aside by their distance from it; lock when
// enough pulses lie close enough to it; a run of outliers on one side a jump, re-acquired; a source silent too long lost, held, then
// stopped. Every constant is part G's provisional one, in FollowConfig, to be set by its tests and the loopback.

// FollowConfig holds the follower's constants (ADR-0004 part G's table).
type FollowConfig struct {
	// Window is the most pulses the line is fitted through once locked; MinLock the fewest it locks on.
	Window, MinLock int
	// Outliers lie beyond Outlier times the residuals' median absolute deviation, or beyond OutlierFloor of an interval, the larger.
	Outlier, OutlierFloor float64
	// JumpRun outliers in a row on one side are a jump when their own interval is more than JumpChange off the line's; less, a ramp
	// the line catches up with (part C: a ramp is not a jump).
	JumpRun    int
	JumpChange float64
	// MissingGap is how many intervals past the line's place for the next pulse a pulse must come to count the ones between as missing:
	// above two, so half time is a jump and a late pulse is late, not a missing one.
	MissingGap float64
	// The source is lost after LossPulses intervals or LossFloor, the longer, with no pulse.
	LossPulses float64
	LossFloor  time.Duration
	// HoldBeats is how long a lost source's tempo is held before the followers are stopped.
	HoldBeats int
}

// DefaultFollow is part G's provisional constants.
var DefaultFollow = FollowConfig{
	Window: 96, MinLock: 24, Outlier: 3, OutlierFloor: 0.1, JumpRun: 6, JumpChange: 0.1, MissingGap: 2.5,
	LossPulses: 4, LossFloor: 120 * time.Millisecond, HoldBeats: 8,
}

// The follower's states.
const (
	FollowAcquiring = "acquiring"
	FollowLocked    = "locked"
	FollowHolding   = "holding"
	FollowLost      = "lost"
)

// FollowCounts are what the follower counted (ADR-0004 part D's list, its own part).
type FollowCounts struct {
	Pulses, Outliers, Jumps, Ramps, Losses, Holds, Reacquired, Missing uint64
	// Held counts the gaps a burst of pulses then filled: a delivery the system held, not pulses missing
	Held uint64
	// Rephased counts the Starts whose first pulse moved the line's phase; LockedAfter is how many pulses it heard before it first locked
	// (part G's time to lock), 0 while it has not
	Rephased, LockedAfter uint64
}

type pulsePoint struct {
	index int64 // its place in the source's count since acquiring began
	at    int64
}

// Follower follows one source's clock.
type Follower struct {
	c      FollowConfig
	state  string
	points []pulsePoint
	// the line: at(index) = origin + interval × index; spread its inliers' robust spread, all in nanoseconds
	origin, interval, spread float64
	fitted                   bool
	last                     pulsePoint
	heard                    bool
	outRun                   int          // outliers in a row, signed by side
	run                      []pulsePoint // they
	// moving: the source left the line, MovingAfter outliers in a row on one side, and has not come back to it for MovingAfter pulses in
	// a row (inRun) since, through any ramp the line was refitted for meanwhile
	moving bool
	inRun  int
	// drift: the residuals of the last pulses on the line, which a slow ramp moves to one side (drifting) while none is an outlier
	drift     []float64
	drifting  bool
	holdUntil int64
	counts    FollowCounts
	// waiting: pulses held back elsewhere (by the echo test) and not learned yet; no lock until they are, since the indices of the pulses
	// after them close the gaps they leave, and a line locked on those would put every later pulse a slot off
	waiting bool
	// gap: a pulse that came past MissingGap intervals while locked, taken as the next in the count until the pulse after it says which it
	// was: on time, the pulses between were missing; within half an interval, a burst, the system held the delivery and stamped the pulses
	// as it let them go (2026-11-16: 58 ms, then three pulses in 6 ms, taken for two missing, put the count two ahead for the rest of a run)
	gap *heldGap
}

// heldGap is a gap not decided yet: the follower as it was before the pulse that ended it, that pulse, and the index a dropout puts it at.
type heldGap struct {
	before Follower
	at     int64
	index  int64
}

// clone is a copy of the follower that shares no slice with it.
func (f *Follower) clone() Follower {
	c := *f
	c.points = append([]pulsePoint(nil), f.points...)
	c.run = append([]pulsePoint(nil), f.run...)
	c.drift = append([]float64(nil), f.drift...)
	c.gap = nil
	return c
}

// Wait says whether pulses are held back elsewhere and not yet learned: while they are, the follower does not lock.
func (f *Follower) Wait(waiting bool) { f.waiting = waiting }

// mayLock is whether the line is good enough, and nothing is held back, to lock on.
func (f *Follower) mayLock() bool {
	return f.state == FollowAcquiring && !f.waiting && len(f.points) >= f.c.MinLock && f.spread <= f.interval/4
}

// NewFollower is a follower with these constants, acquiring.
func NewFollower(c FollowConfig) *Follower { return &Follower{c: c, state: FollowAcquiring} }

// State is acquiring, locked, holding or lost.
func (f *Follower) State() string { return f.state }

// Counts are what it counted.
func (f *Follower) Counts() FollowCounts { return f.counts }

// Interval is the source's pulse interval, nanoseconds, once fitted; 0 before.
func (f *Follower) Interval() float64 {
	if !f.fitted {
		return 0
	}
	return f.interval
}

// Tempo is the source's tempo, BPM, once fitted; 0 before.
func (f *Follower) Tempo() float64 {
	if !f.fitted || f.interval <= 0 {
		return 0
	}
	return float64(time.Minute) / (f.interval * 24)
}

// Spread is the inliers' robust spread around the line, nanoseconds.
func (f *Follower) Spread() float64 { return f.spread }

// Next is the time of the source's pulse after the last it heard, on the line; 0 before a line is fitted.
func (f *Follower) Next() int64 { return f.At(f.last.index + 1) }

// At is the time of the source's pulse of this index, on the line.
func (f *Follower) At(index int64) int64 {
	if !f.fitted {
		return 0
	}
	return int64(f.origin + f.interval*float64(index))
}

// LastIndex is the index of the last pulse heard, in the source's count since acquiring began.
func (f *Follower) LastIndex() int64 { return f.last.index }

// LastAt is when the last pulse was heard.
func (f *Follower) LastAt() int64 { return f.last.at }

// MovingAfter is how many outliers in a row on one side make the source moving, and how many pulses back on the line end it: more than
// one, so a single spike is still smoothed.
const MovingAfter = 2

// Moving says whether the source is changing its tempo faster than the locked line follows: MovingAfter outliers in a row on one side,
// and not yet MovingAfter pulses in a row back on the line, through the ramps the line is refitted for meanwhile. While it is, the
// source's own pulses are the better guide than a line a step behind them.
func (f *Follower) Moving() bool {
	return f.state == FollowLocked && (f.moving || f.drifting || f.Uneven())
}

// UnevenSpread is the line's spread, as a share of the interval, past which a source is too uneven to re-make: a line through it fits
// none of its pulses, so its own are sent on, each at its place. A synth under 30 BPM sent pulses 83, 84, 83, 83 and 167 ms apart (a
// spread of 30% of its 100.8 ms interval), and re-made on the line they went out up to 33 ms off its own (2026-11-15); a DAW's jitter
// of 2 ms RMS at 120 BPM is under a tenth.
const UnevenSpread = 1.0 / 6

// Uneven says whether the source's own pulses lie too far about its line to be re-made on it (UnevenSpread).
func (f *Follower) Uneven() bool {
	return f.fitted && f.interval > 0 && f.spread > UnevenSpread*f.interval
}

// DriftPulses is the run of pulses on the line whose mean residual tells a slow ramp: a beat. DriftOn is the mean, as a share of the
// interval, past which the source drifts off the line on one side (a ramp the line's own fit lags), and half of it where that ends.
const (
	DriftPulses = 24
	DriftOn     = 0.04
)

// drifted takes the residual of a pulse on the line: over the last beat's, a mean past DriftOn of the interval, or the line's own spread
// if larger, is a slow ramp the line lags by about half its window, and the source is moving until the mean falls under half that.
func (f *Follower) drifted(residual float64) {
	if f.drift = append(f.drift, residual); len(f.drift) > DriftPulses {
		f.drift = f.drift[len(f.drift)-DriftPulses:]
	}
	if len(f.drift) < DriftPulses {
		return
	}
	mean := 0.0
	for _, r := range f.drift {
		mean += r
	}
	mean = math.Abs(mean / float64(len(f.drift)))
	on := math.Max(DriftOn*f.interval, f.spread)
	switch {
	case mean > on:
		f.drifting = true
	case mean < on/2:
		f.drifting = false
	}
}

// CameLate says whether the source's last pulse came late off the locked line: the first sign of a slower tempo, or a spike. Either way
// its next pulse is better waited for than sent on the line's guess: on time, it comes at its place anyway.
func (f *Follower) CameLate() bool { return f.state == FollowLocked && f.outRun > 0 }

// Reset forgets the source: acquiring again from its next pulse.
func (f *Follower) Reset() {
	f.state, f.points, f.fitted, f.outRun, f.run, f.spread, f.last, f.heard = FollowAcquiring, nil, false, 0, nil, 0, pulsePoint{}, false
	f.moving, f.inRun, f.drift, f.drifting, f.gap = false, 0, nil, false, nil
}

// Restarted takes the source's first pulse after Start or Continue. A device whose clock runs while stopped may restart it at Start, its
// first pulse a few milliseconds after the last one stopped (seen on a real device: 4 to 14 ms of a 20.8 ms interval), so the phase the
// line learned no longer holds while its interval does. When the pulse lies off the line beyond the outlier threshold, the line and the
// pulses it was fitted on are moved to it, interval and spread kept; on the line, it is an ordinary pulse.
func (f *Follower) Restarted(at int64) {
	f.gap = nil
	if f.heard && f.fitted && f.interval > 0 && f.state != FollowLost {
		place := f.origin + f.interval*float64(f.last.index+1)
		if delta := float64(at) - place; math.Abs(delta) > f.threshold() {
			shift := int64(delta)
			for i := range f.points {
				f.points[i].at += shift
			}
			f.origin += delta
			f.last.at += shift
			f.outRun, f.run = 0, nil
			f.counts.Rephased++
			f.drift = nil
		}
	}
	f.Pulse(at)
}

// Pulse takes one pulse of the source, heard at this time. Its index is the next in the source's count, or, when it comes more than
// MissingGap intervals after the last one heard and after the line's place for it, the one the line puts nearest, so a source's dropout does not shift the
// count the line is fitted on, while a late pulse, or half time, is not taken for missing ones. While locked, the pulse after such a gap
// decides it: on time, the gap's pulse is taken again in the line's place and the ones between counted missing; in a burst, it stands as
// the next, since the system held the pulses and let them go together.
func (f *Follower) Pulse(at int64) {
	f.settle(at)
	f.take(at, -1)
}

// settle decides a gap not decided yet, by a pulse heard at this time or by the time alone: half an interval after the gap's pulse with
// none in between, no burst can come, so pulses were missing; one sooner is a burst.
func (f *Follower) settle(now int64) {
	g := f.gap
	if g == nil {
		return
	}
	f.gap = nil
	if float64(now-g.at) >= f.interval/2 {
		*f = g.before
		f.take(g.at, g.index)
	} else {
		f.counts.Held++
	}
}

// take is Pulse's work, its index forced when that is not negative.
func (f *Follower) take(at, forced int64) {
	entry := *f
	f.counts.Pulses++
	index := int64(0)
	if f.heard {
		index = f.last.index + 1
		// only a locked line is trusted to say a pulse is missing: while acquiring, one spike would shift every index after it; and not
		// while the source moves off it, drifts or is too uneven for it, when its interval is not the source's: a slowing source's double
		// gap (a synth's uneven clock under 30 BPM) came to more than MissingGap of a lagging line's intervals, and 16 pulses counted as
		// missing were sent on that the source never sent (2026-11-15)
		if f.state != FollowAcquiring && f.fitted && f.interval > 0 && !f.moving && !f.drifting && !f.Uneven() {
			// past the gap both from the last pulse heard and from the line's place for it: a dropout is both, while a pulse after a
			// spike, or half time, is one alone
			gap := math.Min(float64(at-f.last.at), float64(at)-(f.origin+f.interval*float64(f.last.index))) / f.interval
			if gap > f.c.MissingGap && gap < 1e6 {
				switch n := int64(math.Round(gap)); {
				case forced >= 0:
					index = forced
					f.counts.Missing += uint64(forced - f.last.index - 1)
				case f.state == FollowLocked:
					// the next in the count until the pulse after it says
					f.gap = &heldGap{before: entry.clone(), at: at, index: f.last.index + n}
				default:
					index = f.last.index + n
					f.counts.Missing += uint64(n - 1)
				}
			}
		}
	}
	p := pulsePoint{index: index, at: at}
	switch f.state {
	case FollowHolding:
		// back within the hold: the line kept, the count slewed to where the line puts it (part C)
		f.state = FollowLocked
		f.counts.Reacquired++
	case FollowLost:
		// back after the hold: acquired again from scratch, the transport left to the master's next Start (part C)
		f.Reset()
		p.index = 0
	}
	if f.state == FollowLocked && f.fitted {
		residual := float64(at) - (f.origin + f.interval*float64(p.index))
		if math.Abs(residual) > f.threshold() {
			f.counts.Outliers++
			side := 1
			if residual < 0 {
				side = -1
			}
			if f.outRun*side > 0 {
				f.outRun += side
			} else {
				f.outRun, f.run = side, nil
			}
			f.run = append(f.run, p)
			f.last, f.heard = p, true
			if f.inRun = 0; f.outRun*side >= MovingAfter {
				f.moving = true
			}
			if f.outRun*side >= f.c.JumpRun {
				run := f.run
				_, runInterval := line(run, nil)
				if math.Abs(runInterval/f.interval-1) > f.c.JumpChange {
					// the source's change, not noise: acquired again from the run, its pulses keeping their places in the source's
					// count, so a count kept by index (the pulses sent on since Start) is kept across the jump
					f.counts.Jumps++
					f.Reset()
					f.points = append(f.points, run...)
				} else {
					// a ramp the line lags: caught up, the line fitted through the run alone, still locked. Pulses of the tempo the
					// source has left, kept beside the run, held the line a step behind it and widened its spread, and so the threshold,
					// until the source's own pulses 20 ms off the line counted as on it (a tempo turned within a beat, 2026-11-15)
					f.counts.Ramps++
					f.drift = nil // residuals against the line refitted for the ramp start again
					f.points = append([]pulsePoint(nil), run...)
					f.outRun, f.run = 0, nil
				}
				f.last, f.heard = f.points[len(f.points)-1], true
				f.fit()
			}
			return
		}
		f.outRun, f.run = 0, nil
		if f.moving {
			if f.inRun++; f.inRun >= MovingAfter {
				f.moving, f.inRun = false, 0
			}
		}
		f.drifted(residual)
	}
	f.last, f.heard = p, true
	f.points = append(f.points, p)
	if len(f.points) > f.c.Window {
		f.points = f.points[len(f.points)-f.c.Window:]
	}
	f.fit()
	if f.mayLock() {
		f.state = FollowLocked
		if f.counts.LockedAfter == 0 {
			f.counts.LockedAfter = f.counts.Pulses
		}
	}
}

// OnLine says whether a pulse heard at this time lies on the line, locked or still provisional, within the outlier threshold of a slot.
func (f *Follower) OnLine(at int64) bool {
	if !f.fitted || f.interval <= 0 || f.state == FollowLost {
		return false
	}
	index := math.Round((float64(at) - f.origin) / f.interval)
	return math.Abs(float64(at)-(f.origin+f.interval*index)) <= f.threshold()
}

// SlotFilled says whether a pulse heard at this time falls, on the locked line, in a slot whose pulse the follower has already heard: an
// extra pulse there (an echo of what the hub wrote for that slot), where a late pulse of the source falls in the next slot, still empty.
func (f *Follower) SlotFilled(at int64) bool {
	if f.state != FollowLocked || !f.fitted || f.interval <= 0 {
		return false
	}
	return int64(math.Round((float64(at)-f.origin)/f.interval)) <= f.last.index
}

// Late learns a pulse heard earlier and held back (a suspect the echo test cleared), in its place by time among the pulses learned: the
// pulses after it were indexed in their arrival order while it was held, so where its neighbours' indices leave a slot between them it
// takes it (the line's nearest, once there is a line), and where they do not, the gap it left was closed, and the pulses from the next
// one on move up by one to make room. It answers the index from which pulses moved, or -1 when none did, so a count kept by index is kept.
func (f *Follower) Late(at int64) (movedFrom int64) {
	f.counts.Pulses++
	movedFrom = -1
	i := sort.Search(len(f.points), func(i int) bool { return f.points[i].at > at })
	var index int64
	switch {
	case len(f.points) == 0:
		index = 0
	case i == len(f.points):
		// after every pulse learned: the next one
		index = f.points[i-1].index + 1
	case i > 0 && f.points[i].index-f.points[i-1].index >= 2:
		// a slot left between its neighbours: the line's nearest within it, or the first
		low, high := f.points[i-1].index+1, f.points[i].index-1
		index = low
		if f.fitted && f.interval > 0 {
			index = min(high, max(low, int64(math.Round((float64(at)-f.origin)/f.interval))))
		}
	default:
		// no slot left: the gap was closed while it was held; the pulses from the next one on move up by one
		index, movedFrom = f.points[i].index, f.points[i].index
		for k := range f.points {
			if f.points[k].index >= index {
				f.points[k].index++
			}
		}
		if f.last.index >= index {
			f.last.index++
		}
	}
	p := pulsePoint{index: index, at: at}
	f.points = append(f.points, p)
	sort.Slice(f.points, func(i, j int) bool { return f.points[i].index < f.points[j].index })
	if len(f.points) > f.c.Window {
		f.points = f.points[len(f.points)-f.c.Window:]
	}
	if !f.heard || index > f.last.index {
		f.last, f.heard = p, true
	}
	f.fit()
	if f.mayLock() {
		f.state = FollowLocked
		if f.counts.LockedAfter == 0 {
			f.counts.LockedAfter = f.counts.Pulses
		}
	}
	return movedFrom
}

// threshold is the distance beyond which a pulse is an outlier: the larger of the residuals' scaled spread and a floor.
func (f *Follower) threshold() float64 {
	return math.Max(f.c.Outlier*f.spread, f.c.OutlierFloor*f.interval)
}

// fit fits the line through the window, again without the pulses beyond the outlier threshold of the first fit.
func (f *Follower) fit() {
	if len(f.points) < 2 {
		return
	}
	origin, interval := line(f.points, nil)
	residuals := make([]float64, len(f.points))
	for i, p := range f.points {
		residuals[i] = float64(p.at) - (origin + interval*float64(p.index))
	}
	spread := mad(residuals)
	limit := math.Max(f.c.Outlier*spread, f.c.OutlierFloor*interval)
	keep := make([]bool, len(f.points))
	kept := 0
	for i, r := range residuals {
		if keep[i] = math.Abs(r) <= limit; keep[i] {
			kept++
		}
	}
	if kept >= 2 && kept < len(f.points) {
		origin, interval = line(f.points, keep)
		residuals = residuals[:0]
		for i, p := range f.points {
			if keep[i] {
				residuals = append(residuals, float64(p.at)-(origin+interval*float64(p.index)))
			}
		}
		spread = mad(residuals)
	}
	f.origin, f.interval, f.spread, f.fitted = origin, interval, spread, true
}

// Check is the follower's look at the time: a locked source silent past the loss limit is held; a hold past its beats is lost.
// It answers whether the state changed.
// CheckAt is when Check would next change the state with no pulse heard: the loss while locked, the hold's end while holding; 0 when
// nothing would, so the time thread need not ask before a pulse comes.
func (f *Follower) CheckAt() int64 {
	if !f.fitted {
		return 0
	}
	switch f.state {
	case FollowLocked:
		return f.last.at + int64(math.Max(f.c.LossPulses*f.interval, float64(f.c.LossFloor))) + 1
	case FollowHolding:
		return f.holdUntil + 1
	}
	return 0
}

func (f *Follower) Check(now int64) bool {
	if !f.fitted {
		return false
	}
	if f.gap != nil && float64(now-f.gap.at) >= f.interval/2 {
		f.settle(now)
	}
	changed := false
	if f.state == FollowLocked {
		limit := math.Max(f.c.LossPulses*f.interval, float64(f.c.LossFloor))
		if float64(now-f.last.at) > limit {
			f.state, changed = FollowHolding, true
			f.counts.Losses++
			f.counts.Holds++
			f.holdUntil = f.last.at + int64(float64(f.c.HoldBeats*24)*f.interval)
		}
	}
	if f.state == FollowHolding && now > f.holdUntil {
		f.state, changed = FollowLost, true
	}
	return changed
}

// line fits at = origin + interval × index by least squares, through the points kept (all when keep is nil).
func line(points []pulsePoint, keep []bool) (origin, interval float64) {
	var n, sx, sy float64
	base := float64(points[0].at)
	for i, p := range points {
		if keep == nil || keep[i] {
			n++
			sx += float64(p.index)
			sy += float64(p.at) - base
		}
	}
	mx, my := sx/n, sy/n
	var sxx, sxy float64
	for i, p := range points {
		if keep == nil || keep[i] {
			dx := float64(p.index) - mx
			sxx += dx * dx
			sxy += dx * (float64(p.at) - base - my)
		}
	}
	if sxx == 0 {
		return float64(points[0].at), 0
	}
	interval = sxy / sxx
	return base + my - interval*mx, interval
}

// mad is the median absolute deviation from the median, scaled to a standard deviation's (× 1.4826).
func mad(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	median := func(v []float64) float64 {
		s := append([]float64(nil), v...)
		sort.Float64s(s)
		if len(s)%2 == 1 {
			return s[len(s)/2]
		}
		return (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
	m := median(values)
	dev := make([]float64, len(values))
	for i, v := range values {
		dev[i] = math.Abs(v - m)
	}
	return 1.4826 * median(dev)
}
