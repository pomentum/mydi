package mydi

import (
	"math"
	"time"
)

// The clock sent on (ADR-0004 part D), as plain code around the follower: a source's messages in with their times, and what the
// followed ports should be sent and when, out, asked for by the time thread as time passes. Its rules, each a test of part G:
//
//   - The count is kept: between Start (or Continue) and Stop, the pulses sent equal the pulses the source sent. A stall of the output
//     is caught up with pulses no closer than a quarter interval, never skipped; the hand-over from relayed to re-made pulses neither
//     adds nor drops one; a slew of the offset changes the spacing, never the count.
//   - While the follower acquires, each source pulse is relayed one for one; once it is locked, the pulses are the line's, each waited
//     for from the source up to a tolerance past its place, so a Stop that comes in place of the next pulse finds none sent past it. A
//     Stop later than that, or an offset that sends early, can find one: counted as added, never hidden.
//   - Start and Continue go out at once; the first pulse after them is the source's first after them, never before them, whatever the
//     offset, which is brought in only afterwards, gradually.
//   - Stop goes out at once, after any pulse still owed; Song Position only while stopped.
//   - No two pulses go out closer than a quarter of the source's interval, or 2 ms, the larger: counted ones are caught up at
//     that spacing, and one relayed while stopped that comes sooner is dropped, so a flood or a loop never floods the followed ports.
//   - A source lost is held for its beats with the line's pulses, then Stop is sent; one back within the hold has the difference caught
//     up when under a beat, and reported when not.

// RemakeConfig is the re-made clock's settings: the offset the pulses are sent early by (late when negative), at most 20 ms either way,
// brought in by at most an eighth of an interval a pulse; the echo test in front of the follower, when the hub clocks anything that could
// come back into the port followed (part A); and the dither on its pulses, the fallback signature, when the echo test finds the hub's
// own fingerprint too faint.
type RemakeConfig struct {
	Offset time.Duration
	Echo   *EchoConfig
	Dither time.Duration
}

// suspect is a pulse the echo test could not yet say was real, kept aside: neither learned nor relayed.
type suspect struct {
	at    int64
	heard int
}

// Send is a message to send to the followed ports, and when.
type Send struct {
	At      int64
	Message []byte
}

// RemakeCounts are what the re-made clock counted (part D's list).
type RemakeCounts struct {
	// Received and Sent are the pulses of the source and of the hub between Start and Stop; Added the pulses sent past the source's at
	// a Stop, CaughtUp those sent late after a stall, Reported a difference of a beat or more after a hold, told and not caught up.
	Received, Sent, Added, CaughtUp, Reported int64
	// PositionsDropped are Song Positions that came while running; StopsOnLoss the Stops sent for a source lost past its hold;
	// RelaysDropped the pulses that came while stopped closer than a quarter interval after the last one sent on.
	PositionsDropped, StopsOnLoss, RelaysDropped int64
	// Echoes are the hub's own pulses come back, refused; Suspects the pulses held aside until the echo test could say.
	Echoes, Suspects int64
}

// Remaker re-makes one source's clock for the ports that follow it.
type Remaker struct {
	f      *Follower
	offset float64 // the target, nanoseconds
	off    float64 // the offset applied now, slewed to the target

	running   bool
	waitFirst bool  // after Start or Continue: the next pulse is the source's own
	startedAt int64 // when Start or Continue went out
	base      int64 // the follower's index of the first pulse after Start, so the source's count is LastIndex - base + 1
	sent      int64
	lastSent  int64
	counts    RemakeCounts

	echo  *Echo
	held  []suspect
	heard int
	// recent are the pulses heard since Start or Continue that the echo test did not refuse, in their order; own are this re-maker's
	// pulses not yet told as written; foreign is when the hub last wrote a pulse that was not this re-maker's (another clock of the hub)
	recent  []int64
	own     []int64
	foreign int64
	dither  time.Duration
	writes  uint64
	// backSeen is the follower's count of sources back within a hold, as last looked at: only such a return may report a difference
	backSeen uint64
	// wantGuard is the echo test asked on or off and not applied yet
	wantGuard *bool
}

// NewRemaker re-makes the clock of the source this follower follows.
func NewRemaker(f *Follower, c RemakeConfig) *Remaker {
	offset := math.Max(-20e6, math.Min(20e6, float64(c.Offset)))
	r := &Remaker{f: f, offset: offset, dither: c.Dither}
	if c.Echo != nil {
		r.echo = NewEcho(*c.Echo)
	}
	return r
}

// Wrote is told each pulse the hub wrote, at the time it wrote it, by whichever clock: the echo test knows the hub's fingerprint from
// these alone, never from the times it aimed for.
func (r *Remaker) Wrote(at int64) {
	if r.echo == nil {
		return
	}
	r.echo.Wrote(at)
	// one of this re-maker's own pulses, written within a few milliseconds of its time, or another clock's
	mine := false
	kept := r.own[:0]
	for _, o := range r.own {
		switch {
		case !mine && abs64(o-at) <= ownMatch:
			mine = true
		case o >= at-ownMatch:
			kept = append(kept, o)
		}
	}
	r.own = kept
	if !mine {
		r.foreign = at
	}
}

// ownMatch is how far from its time a pulse this re-maker sent may be written and still be known as its own: the time thread's lateness
// and the dither are well within it.
const ownMatch = int64(5 * time.Millisecond)

// sent records one of this re-maker's own pulses, so the write told for it is not taken for another clock's.
func (r *Remaker) sentOwn(s Send) Send {
	if r.echo != nil {
		if r.own = append(r.own, s.At); len(r.own) > 256 {
			r.own = r.own[len(r.own)-128:]
		}
	}
	return s
}

// guard turns the echo test on or off (ADR-0005 part B): at once while the source's transport is stopped, else at its next Start or
// Continue, since the pulses it holds aside are part of the count.
func (r *Remaker) guard(on bool) {
	r.wantGuard = &on
	if !r.running {
		r.applyGuard()
	}
}

func (r *Remaker) applyGuard() {
	if r.wantGuard == nil {
		return
	}
	on := *r.wantGuard
	r.wantGuard = nil
	switch {
	case on && r.echo == nil:
		r.echo = NewEcho(DefaultEcho)
	case !on && r.echo != nil:
		r.echo, r.held, r.own, r.recent = nil, nil, nil, r.recent[:0]
		r.f.Wait(false)
	}
}

// Echo is the echo test in front of the follower, when there is one.
func (r *Remaker) Echo() *Echo { return r.echo }

// dithered is a pulse's time with the dither's offset for it, when the dither is on.
func (r *Remaker) dithered(at int64) int64 {
	if r.dither <= 0 {
		return at
	}
	r.writes++
	return at + Dither(r.writes, r.dither)
}

// Counts are what it counted.
func (r *Remaker) Counts() RemakeCounts {
	c := r.counts
	c.Sent = r.sent
	c.Received = r.position()
	return c
}

// Running says whether the source's transport runs.
func (r *Remaker) Running() bool { return r.running }

// position is the source's count since Start: its pulses heard, and those the line says it sent and did not reach the hub.
func (r *Remaker) position() int64 {
	if !r.running || r.waitFirst {
		return 0
	}
	return r.f.LastIndex() - r.base + 1
}

// quarter is the least spacing of two pulses sent: a quarter of the source's interval, never under a quarter interval at 300 BPM (about
// 2 ms), the fastest clock followed, so a flood that the follower takes for a jump to a tiny interval does not lower it.
func (r *Remaker) quarter() int64 {
	return max(int64(r.f.Interval()/4), int64(2*time.Millisecond))
}

// Message takes one message of the source, heard at this time, and answers what goes out at once.
func (r *Remaker) Message(message []byte, at int64) []Send {
	if len(message) == 0 {
		return nil
	}
	switch message[0] {
	case 0xF8:
		return r.heardPulse(at)
	case 0xFA, 0xFB: // Start, Continue: out at once; the next pulse is the source's own
		r.applyGuard()
		r.running, r.waitFirst, r.startedAt, r.sent, r.off, r.recent = true, true, at, 0, 0, r.recent[:0]
		return []Send{{At: at, Message: []byte{message[0]}}}
	case 0xFC: // Stop: what is still owed first, never closer than a quarter interval, then Stop
		var out []Send
		if r.running {
			for owed := r.position() - r.sent; owed > 0; owed-- {
				out = append(out, r.send(at, true))
			}
			if r.sent > r.position() {
				r.counts.Added += r.sent - r.position()
			}
		}
		r.running = false
		r.applyGuard()
		stopAt := at
		if len(out) > 0 {
			stopAt = out[len(out)-1].At
		}
		return append(out, Send{At: stopAt, Message: []byte{0xFC}})
	case 0xF2: // Song Position: only while stopped
		if r.running {
			r.counts.PositionsDropped++
			return nil
		}
		return []Send{{At: at, Message: append([]byte(nil), message...)}}
	}
	return nil
}

// heardPulse puts each source pulse through the echo test first, when there is one (part A). An echo is refused and counted. A doubtful
// pulse (suspect, or unknown for want of evidence) on the follower's line, locked or provisional, is the master's. Off it, once locked: an
// unknown one in the next slot, still empty, is the master's pulse come late; any other refused, since an echo is an extra in a slot whose
// pulse was heard. While acquiring, held aside, neither learned nor relayed, so the hub never locks on its own clock nor sends it back out.
// Held pulses are judged again with each pulse heard: an echo dropped, a real one or one past the decision's window learned late, in its
// place.
func (r *Remaker) heardPulse(at int64) []Send {
	if r.echo == nil {
		return r.pulse(at)
	}
	r.heard++
	verdict := r.echo.Judge(at)
	if verdict != EchoYes {
		if r.recent = append(r.recent, at); len(r.recent) > 32 {
			r.recent = r.recent[len(r.recent)-32:]
		}
	}
	r.settleHeld()
	doubtful := verdict == EchoSuspect || verdict == EchoUnknown
	switch {
	case doubtful && r.running && r.waitFirst:
		// the first pulse after Start or Continue is the source's own: the hub has written none since, so only an echo the test has
		// proven is refused; a device that restarts its clock at Start sends it a few milliseconds after its last, in a slot the line
		// had filled, which would otherwise be taken for an extra and put the whole count a pulse behind
	case verdict == EchoYes && r.f.State() == FollowLocked && !r.f.SlotFilled(at):
		// at the echo's delay by chance, but in the next slot, still empty: the master's pulse; an echo is an extra in a filled one
	case verdict == EchoYes:
		r.counts.Echoes++
		return nil
	case doubtful && r.f.Moving():
		// the source changing its tempo faster than the line follows: the line's slots are not the source's, and a pulse it has moved
		// half an interval early falls in one the line had filled; taken for an extra, a jump to a faster tempo was refused pulse by
		// pulse and never learned (2026-11-15). The hub writes each pulse after the source's while it moves, so only a proven echo is one
	case doubtful && r.f.OnLine(at):
		// on the line, locked or provisional: the master's, as the hub's own relays make every master pulse pair with the write before it
	case doubtful && r.f.State() == FollowLocked && !r.f.SlotFilled(at):
		// off the line, in the next slot still empty: the master's pulse, late; an echo is an extra in a slot whose pulse was heard
	case doubtful && r.f.State() == FollowLocked:
		r.counts.Echoes++
		return nil
	case doubtful:
		r.held = append(r.held, suspect{at: at, heard: r.heard})
		r.f.Wait(true)
		r.counts.Suspects++
		return nil
	}
	return r.pulse(at)
}

// settleHeld judges the held pulses again: an echo dropped; a real one, one in an even run of the master's, or one held past the
// decision's window, learned in its place. While any is held, the follower does not lock.
func (r *Remaker) settleHeld() {
	defer func() { r.f.Wait(len(r.held) > 0) }()
	if len(r.held) == 0 {
		return
	}
	r.f.Wait(true)
	master := r.mastersFrom()
	kept := r.held[:0]
	for _, h := range r.held {
		switch verdict := r.echo.Resolve(h.at); {
		case verdict == EchoYes:
			r.counts.Echoes++
		case verdict == EchoNone || (master > 0 && h.at >= master) || r.heard-h.heard > r.echo.c.Window+8:
			// learned in its place; when that moved the pulses from the first after Start on, the count's base moves with them
			if from := r.f.Late(h.at); from >= 0 && r.running && !r.waitFirst && from <= r.base {
				r.base++
			}
		default:
			kept = append(kept, h)
		}
	}
	r.held = kept
}

// evenRun is the time from which the pulses heard since Start form an even run, four or more in a row whose spacings are each within a
// quarter of their median (and a millisecond) of it, or 0. Such a run is the master's: an echo is an extra inside one of the master's
// intervals I, splitting it into d and I-d, and with the master's next spacing I one of the three is at least I/3 from their median,
// never within a quarter of it, so no run that holds an echo is even, as long as every pulse the hub wrote that could come back into it was this
// re-maker's own relay of the source. A pulse of another clock of the hub's, written within the echo test's reach before the run began
// or since, could be one an even run is made of (the hub's own clock coming back), and then no run counts.
func (r *Remaker) evenRun() (start int64, step float64) {
	n := len(r.recent)
	gaps := make([]float64, 0, n)
	for k := n; k >= 4; k-- {
		run := r.recent[n-k:]
		if r.foreign != 0 && r.foreign >= run[0]-int64(r.echo.c.MaxDelay) {
			continue
		}
		gaps = gaps[:0]
		for i := 1; i < k; i++ {
			gaps = append(gaps, float64(run[i]-run[i-1]))
		}
		median := spreadOf(gaps).Median
		tolerance := math.Max(1e6, median/4)
		even := true
		for _, g := range gaps {
			if math.Abs(g-median) > tolerance {
				even = false
				break
			}
		}
		if even {
			return run[0], median
		}
	}
	return 0, 0
}

// mastersFrom is the time from which the held pulses are the master's, or 0: those in an even run, and those before it back to the last
// pulse learned, when they are as many as the master's spacing puts between that pulse and the run, none closer than half a spacing to
// the one before: a pulse jittered off the run is the master's, while an echo is one more than the spacing holds.
func (r *Remaker) mastersFrom() int64 {
	start, step := r.evenRun()
	if start == 0 {
		return 0
	}
	i := len(r.recent) - 1
	for i >= 0 && r.recent[i] > start {
		i--
	}
	// back from the run's first pulse to the last one not held
	j := i - 1
	for j >= 0 && r.isHeld(r.recent[j]) {
		j--
	}
	if j < 0 || j == i-1 {
		return start
	}
	from, to := r.recent[j], r.recent[i]
	if between := i - j - 1; float64(between) != math.Round(float64(to-from)/step)-1 {
		return start
	}
	for k := j + 1; k <= i; k++ {
		if float64(r.recent[k]-r.recent[k-1]) < step/2 {
			return start
		}
	}
	return r.recent[j+1]
}

func (r *Remaker) isHeld(at int64) bool {
	for _, h := range r.held {
		if h.at == at {
			return true
		}
	}
	return false
}

// pulse is one source pulse: learned by the follower; relayed when it is the first after Start, or while the follower acquires, or while
// the transport is stopped (the ports kept clocked, uncounted); left to the line once locked.
func (r *Remaker) pulse(at int64) []Send {
	before := r.f.State()
	if r.running && r.waitFirst {
		r.f.Restarted(at)
	} else {
		r.f.Pulse(at)
	}
	switch {
	case !r.running:
		if r.lastSent != 0 && at < r.lastSent+r.quarter() {
			// faster than a quarter of the source's interval, or 2 ms: no clock, a flood, perhaps a loop; uncounted
			// while stopped, so dropped, and the output never runs faster than a counted clock may
			r.counts.RelaysDropped++
			return nil
		}
		return []Send{r.relay(at)}
	case r.waitFirst:
		r.waitFirst, r.base = false, r.f.LastIndex()
		return []Send{r.send(max(at, r.startedAt), false)}
	case before == FollowAcquiring || r.f.State() == FollowAcquiring:
		// relayed one for one while acquiring, and on the pulse that locks, so the line takes over from the next; pulses learned late
		// (held, then found the master's) caught up at once, a quarter interval apart
		var out []Send
		for r.position() > r.sent {
			out = append(out, r.send(at, false))
		}
		return out
	}
	return nil
}

// relay is a pulse sent on as it came, counting nothing.
func (r *Remaker) relay(at int64) Send {
	r.lastSent = at
	return r.sentOwn(Send{At: r.dithered(at), Message: []byte{0xF8}})
}

// send is the next counted pulse, at this time or a quarter interval after the last, the later.
func (r *Remaker) send(at int64, late bool) Send {
	if earliest := r.lastSent + r.quarter(); r.sent > 0 && at < earliest {
		at = earliest
		late = true
	}
	if late {
		r.counts.CaughtUp++
	}
	r.sent++
	r.lastSent = at
	return r.sentOwn(Send{At: r.dithered(at), Message: []byte{0xF8}})
}

// tolerance is how long past its place on the line a source's pulse is waited for before the line's goes out without it: three times the
// source's spread, at least a millisecond, at most a quarter interval.
func (r *Remaker) tolerance() int64 {
	return min(r.quarter(), max(int64(time.Millisecond), int64(3*r.f.Spread())))
}

// NextDue is when Due should next be asked, so the time thread waits for it rather than asking every moment: the line's next pulse once
// locked or holding (its place, with the tolerance while the source's own is not heard yet), or when the source would be lost or its
// hold end; 0 when nothing is due until a message comes.
func (r *Remaker) NextDue() int64 {
	next := r.f.CheckAt()
	state := r.f.State()
	if r.running && !r.waitFirst && (state == FollowLocked || state == FollowHolding) {
		if at, wait := r.placeOf(r.base+r.sent, state); !wait && (next == 0 || at < next) {
			next = at
		}
	}
	return next
}

// placeOf is when the pulse of this index in the source's count goes out, or wait when it waits for the source's own pulse to come.
// On the line, steady: its place there. The source's pulse not heard yet: sent early by an offset, at most one ahead; otherwise waited
// for up to a tolerance past its place, so a Stop that comes in its place finds no pulse sent past it, and the pulses go out on the line,
// not on the source's jitter, which arrives within the tolerance. While the source moves off the line (Moving: a tempo changed, which
// the line takes only after JumpRun outliers), the source's own pulses are followed: one heard goes out when it came, and one not heard
// yet is waited for, never sent ahead of the source on a tempo it has left. After a pulse that came late, the next is waited for too.
func (r *Remaker) placeOf(k int64, state string) (at int64, wait bool) {
	at = r.f.At(k) - int64(r.off)
	if state != FollowLocked {
		return at, false
	}
	last := r.f.LastIndex()
	switch {
	case (r.f.Moving() || r.f.CameLate()) && k > last:
		return 0, true
	case r.f.Moving():
		return r.f.LastAt(), false
	case k > last && r.off > 0 && k > last+1:
		return 0, true
	case k > last && r.off <= 0:
		return at + r.tolerance(), false
	}
	return at, false
}

// Due answers the line's pulses whose time has come by now, each never before the last by less than a quarter interval, and, for a source
// lost past its hold, Stop. Asked by the time thread as often as it likes.
func (r *Remaker) Due(now int64) []Send {
	r.f.Check(now)
	state := r.f.State()
	if state == FollowLost && r.running {
		r.running = false
		r.applyGuard()
		r.counts.StopsOnLoss++
		return []Send{{At: now, Message: []byte{0xFC}}}
	}
	if !r.running || r.waitFirst || (state != FollowLocked && state != FollowHolding) {
		return nil
	}
	interval := r.f.Interval()
	// a difference of a beat or more, found when the source is back after a hold, is told, and the count taken from where the source is;
	// any other difference (pulses owed from acquiring, which held some aside) is caught up, since where the source is was never in doubt
	back := r.f.Counts().Reacquired != r.backSeen
	r.backSeen = r.f.Counts().Reacquired
	if diff := r.position() - r.sent; back && state == FollowLocked && (diff >= 24 || diff <= -24) {
		r.counts.Reported += diff
		r.sent += diff
	}
	var out []Send
	for {
		at, wait := r.placeOf(r.base+r.sent, state)
		if wait || at > now {
			return out
		}
		// owed and late: a stall of the output, caught up
		late := now-at > int64(interval/2)
		out = append(out, r.send(max(at, r.startedAt+1), late))
		if r.lastSent > now {
			return out
		}
		// the offset brought in by an eighth of an interval a pulse
		if step := interval / 8; r.off < r.offset {
			r.off = math.Min(r.offset, r.off+step)
		} else if r.off > r.offset {
			r.off = math.Max(r.offset, r.off-step)
		}
	}
}
