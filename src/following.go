package src

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Following is one followed clock on the hub (ADR-0004 part D on the time thread, step 3): a port's clock read with its stamps, fed to
// a re-maker, and what the re-maker sends written to the followed ports by the time thread, at its time, the echo test told when each
// pulse was written. The thread asks the re-maker what is due with one job of its own, aimed at the re-maker's next due time and aimed
// again whenever a message comes, so it never polls.
type Following struct {
	h  *Hub
	in string

	mu sync.Mutex
	// the followed ports and their outputs, under mu, each list replaced whole when a port is added or taken off
	outs  []string
	ports []*out
	re    *Remaker
	// tell turns what happens into the events listeners hear (part E)
	tell    teller
	due     *job
	input   Input
	stopped bool
	// gone: the port was gone at the last look and its input closed; reopened counts its returns
	gone     bool
	reopened uint64

	// what the report compares: the source's pulses and the hub's written ones since the last Start or Continue, by their place in
	// the count, and the stamps each source pulse had
	source, written []int64
	lags            []float64 // each source pulse's delivery: the callback's stamp less the system's, ms
	stamps          StampCounts

	// recording, once asked for: every transport and clock message heard and written, in order, up to maxKept
	recording bool
	record    []Recorded
}

// Recorded is one transport or clock message of a following, heard or written: what a recorded run is replayed from.
type Recorded struct {
	// Out is a message the hub wrote; else one it heard
	Out bool `json:"out,omitempty"`
	// At is the time the following used: a heard message's stamp (part B), a written one's moment of writing; Driver and Received are a
	// heard message's system and callback stamps (Driver -1: none), all nanoseconds on the hub's timeline
	At       int64  `json:"at"`
	Driver   int64  `json:"driver,omitempty"`
	Received int64  `json:"received,omitempty"`
	Message  string `json:"message"`
	// Refused is a heard pulse the echo test refused
	Refused bool `json:"refused,omitempty"`
}

// Record starts recording the following's transport and clock, heard and written, for Recording to answer.
func (f *Following) Record() {
	f.mu.Lock()
	f.recording = true
	f.mu.Unlock()
}

// Recording is what was recorded so far.
func (f *Following) Recording() []Recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Recorded(nil), f.record...)
}

// recordLocked keeps one message; with the following's lock held.
func (f *Following) recordLocked(r Recorded) {
	if f.recording && len(f.record) < 2*maxKept {
		f.record = append(f.record, r)
	}
}

// StampCounts are how the input's times were taken (part B): on the system's stamp refined by the callback's own when the two agree
// within the system's millisecond, on the system's stamp alone when the callback lagged it, or on the callback's where the system gives
// no stamp.
type StampCounts struct {
	Refined, SystemOnly, CallbackOnly uint64
}

// maxKept is how many pulses of each side a report compares: a little over an hour at 120 BPM.
const maxKept = 200000

// ErrFollowSamePort is a port asked to be followed and clocked at once (part A: no clock goes out of the port being followed).
var ErrFollowSamePort = errors.New("a port followed is never clocked: the clock would come back as its own input")

// Follow follows the clock that comes into one port and sends it on, re-made, to others. The input is the follower's alone while it
// runs: its other messages still reach the listeners that listen.
func (h *Hub) Follow(in string, outs []string, c RemakeConfig, fc FollowConfig) (*Following, error) {
	if !h.allowed(in) {
		return nil, ErrNotAllowed
	}
	if len(outs) == 0 {
		return nil, errors.New("following sends the clock to one port or more")
	}
	f := &Following{h: h, in: in, outs: append([]string(nil), outs...), re: NewRemaker(NewFollower(fc), c), tell: newTeller(FollowOptions{})}
	for _, id := range outs {
		if id == in {
			return nil, ErrFollowSamePort
		}
		o, err := h.output(id)
		if err != nil {
			return nil, fmt.Errorf("the output %s: %w", id, err)
		}
		f.ports = append(f.ports, o)
	}
	f.due = &job{id: in, index: -1, follow: f}
	// a port listened to for its events (Listen) is taken over: the following hands its other messages to the same listeners
	h.mu.Lock()
	if _, followed := h.follows[in]; followed {
		h.mu.Unlock()
		return nil, fmt.Errorf("the input %s is followed already", in)
	}
	if listened, open := h.ins[in]; open {
		_ = listened.Close()
		delete(h.ins, in)
	}
	// the port is the following's from here, so Listen does not open it again meanwhile
	h.follows[in] = f
	h.mu.Unlock()
	input, err := f.open()
	if err != nil {
		h.mu.Lock()
		delete(h.follows, in)
		h.mu.Unlock()
		return nil, fmt.Errorf("the input %s: %w", in, err)
	}
	f.mu.Lock()
	f.input = input
	f.mu.Unlock()
	h.mu.Lock()
	h.ins[in] = input
	h.mu.Unlock()
	return f, nil
}

// ErrFollowClocked is a port asked to be followed while the hub clocks it, by its own clock or a following: its clock would come back.
var ErrFollowClocked = errors.New("a port the hub clocks is never followed: its own clock would come back as the source")

// FollowFrom makes a port follow the clock that comes into another (ADR-0004 part A, `clock.follow`): its own clock stopped, taken off
// any source it followed, and added to the source's following, which starts with it. A port follows one source; a source may be followed
// by several ports, and its options are the last asked. The port's clock goes back to its own with Clock.
func (h *Hub) FollowFrom(port, source string, o FollowOptions) error {
	if err := o.check(); err != nil {
		return err
	}
	if port == source {
		return ErrFollowSamePort
	}
	if !h.allowed(port) || !h.allowed(source) {
		return ErrNotAllowed
	}
	h.followMu.Lock()
	defer h.followMu.Unlock()
	h.mu.Lock()
	_, followed := h.follows[port]
	f := h.follows[source]
	clocked := h.outs[source]
	h.mu.Unlock()
	// no clock out of a port followed (part A): the port is itself a source, or the source is a port the hub clocks
	if followed {
		return ErrFollowSamePort
	}
	if clocked != nil {
		clocked.mu.Lock()
		own := clocked.clock != nil
		clocked.mu.Unlock()
		if own {
			return ErrFollowClocked
		}
	}
	if h.followingOf(source) != nil {
		return ErrFollowClocked
	}
	if g := h.followingOf(port); g != nil && g != f {
		h.unfollow(g, port)
	}
	o2, err := h.output(port)
	if err != nil {
		return err
	}
	if err := h.stopClock(o2); err != nil {
		return err
	}
	if f == nil {
		f, err = h.Follow(source, []string{port}, RemakeConfig{}, DefaultFollow)
		if err != nil {
			return err
		}
	} else {
		f.add(port, o2)
	}
	f.configure(o)
	return nil
}

// Unfollow takes a port off the source it follows, if it follows one, with Stop when that source's transport runs; a following left with
// no port ends. It says whether the port followed one.
func (h *Hub) Unfollow(port string) bool {
	h.followMu.Lock()
	defer h.followMu.Unlock()
	g := h.followingOf(port)
	if g == nil {
		return false
	}
	h.unfollow(g, port)
	return true
}

func (h *Hub) unfollow(g *Following, port string) {
	removed, left, running := g.remove(port)
	if !removed {
		return
	}
	if running {
		h.mu.Lock()
		o := h.outs[port]
		h.mu.Unlock()
		if o != nil {
			o.mu.Lock()
			_ = h.handLocked(o, "", entry{msg: []byte{0xFC}})
			o.mu.Unlock()
		}
	}
	if left == 0 {
		g.Stop()
	}
}

// followingOf is the following a port is clocked by, if any.
func (h *Hub) followingOf(port string) *Following {
	h.mu.Lock()
	follows := make([]*Following, 0, len(h.follows))
	for _, f := range h.follows {
		follows = append(follows, f)
	}
	h.mu.Unlock()
	for _, f := range follows {
		if f.has(port) {
			return f
		}
	}
	return nil
}

// Follows are the hub's followings' reports, by the port each follows, and how many ports each clocks.
func (h *Hub) Follows() map[string]FollowReport {
	h.mu.Lock()
	follows := make(map[string]*Following, len(h.follows))
	for in, f := range h.follows {
		follows[in] = f
	}
	h.mu.Unlock()
	out := make(map[string]FollowReport, len(follows))
	for in, f := range follows {
		out[in] = f.Report()
	}
	return out
}

// has says whether the following clocks this port.
func (f *Following) has(port string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.outs {
		if id == port {
			return true
		}
	}
	return false
}

// add clocks one more port from the source; its first pulse is the next the source's count sends, so a port added while the transport
// runs counts from where it joined, and is in step from the source's next Start.
func (f *Following) add(port string, o *out) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.outs {
		if id == port {
			return
		}
	}
	f.outs = append(append([]string(nil), f.outs...), port)
	f.ports = append(append([]*out(nil), f.ports...), o)
}

// remove takes a port off: whether it was on, how many are left, and whether the source's transport runs.
func (f *Following) remove(port string) (removed bool, left int, running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	outs, ports := make([]string, 0, len(f.outs)), make([]*out, 0, len(f.ports))
	for i, id := range f.outs {
		if id == port {
			removed = true
			continue
		}
		outs, ports = append(outs, id), append(ports, f.ports[i])
	}
	if removed {
		f.outs, f.ports = outs, ports
	}
	return removed, len(f.outs), f.re.Running()
}

// configure sets what the follow asks: the hold, the meter and what of the song's place is told, at once; the loop guard from the
// source's next Start or Continue while its transport runs, since pulses the echo test held aside are part of the count.
func (f *Following) configure(o FollowOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hold := DefaultFollow.HoldBeats
	if o.Hold > 0 {
		hold = o.Hold
	}
	f.re.f.c.HoldBeats = hold
	t := newTeller(o)
	f.tell.perBeat, f.tell.inBar, f.tell.beats = t.perBeat, t.inBar, t.beats
	f.re.guard(o.LoopGuard)
}

// tellAll hands the events to the listeners that listen, outside the following's lock.
func (f *Following) tellAll(events []Event) {
	if f.h.listen == nil {
		return
	}
	for _, e := range events {
		f.h.listen(Received{Port: f.in, Event: e, At: time.Now()})
	}
}

// watch is told whether the followed port is there now. Gone, its input is closed; back, it is opened again, with the system's stamps
// mapped afresh. The follower meanwhile goes on as any source silent: held, then lost, then acquired again from the device's next pulse.
func (f *Following) watch(present bool) {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return
	}
	input, gone := f.input, f.gone
	switch {
	case !present && !gone:
		f.input, f.gone = nil, true
		f.mu.Unlock()
		if input != nil {
			_ = input.Close()
		}
		f.h.mu.Lock()
		if f.h.ins[f.in] == input {
			delete(f.h.ins, f.in)
		}
		f.h.mu.Unlock()
		f.h.log("midi: %s, followed, is gone", f.in)
		return
	case present && gone:
		f.mu.Unlock()
		input, err := f.open()
		if err != nil {
			f.h.log("midi: %s, followed, is back but could not be opened: %v", f.in, err)
			return
		}
		f.mu.Lock()
		if f.stopped {
			f.mu.Unlock()
			_ = input.Close()
			return
		}
		f.input, f.gone = input, false
		f.reopened++
		f.mu.Unlock()
		f.h.mu.Lock()
		f.h.ins[f.in] = input
		f.h.mu.Unlock()
		f.h.log("midi: %s, followed, is back", f.in)
		return
	}
	f.mu.Unlock()
}

// open reads the input with the system's stamps where the driver gives them (WinMM), else with the callback's own.
func (f *Following) open() (Input, error) {
	if d, ok := f.h.driver.(StampedDriver); ok {
		return d.OpenInStamped(f.in, func(m Stamped) { f.heard(m) })
	}
	parser := &Parser{}
	var mu sync.Mutex
	return f.h.driver.OpenIn(f.in, func(bytes []byte) {
		at := Now()
		mu.Lock()
		defer mu.Unlock()
		parser.Feed(bytes, func(message []byte) { f.heard(Stamped{Message: message, Driver: -1, Received: at}) })
	})
}

// stampOf is a message's time (part B): the system's stamp is the start of the millisecond it was taken in; the callback's own, when it
// falls within that millisecond, says where in it, and when it falls later the callback was held, so the middle of the millisecond is
// the best there is.
func stampOf(m Stamped) (int64, int) {
	switch {
	case m.Driver < 0:
		return m.Received, 2
	case m.Received >= m.Driver && m.Received < m.Driver+int64(time.Millisecond):
		return m.Received, 0
	default:
		return m.Driver + int64(time.Millisecond)/2, 1
	}
}

// heard is one message of the followed port: the transport and the clock to the re-maker, anything else to the listeners.
func (f *Following) heard(m Stamped) {
	if len(m.Message) == 0 {
		return
	}
	switch status := m.Message[0]; {
	case status == 0xF8 || status == 0xFA || status == 0xFB || status == 0xFC || status == 0xF2:
		at, how := stampOf(m)
		f.mu.Lock()
		if f.stopped {
			f.mu.Unlock()
			return
		}
		switch how {
		case 0:
			f.stamps.Refined++
		case 1:
			f.stamps.SystemOnly++
		default:
			f.stamps.CallbackOnly++
		}
		switch status {
		case 0xFA, 0xFB:
			f.source, f.written, f.lags = f.source[:0], f.written[:0], f.lags[:0]
		case 0xF8:
			if f.re.Running() && len(f.source) < maxKept {
				f.source = append(f.source, at)
				if m.Driver >= 0 {
					f.lags = append(f.lags, float64(m.Received-m.Driver)/1e6)
				}
			}
		}
		echoes := f.re.counts.Echoes
		wasRunning := f.re.Running()
		sends := f.re.Message(m.Message, at)
		f.recordLocked(Recorded{At: at, Driver: m.Driver, Received: m.Received, Message: fmt.Sprintf("%X", m.Message),
			Refused: f.re.counts.Echoes > echoes})
		f.queue(sends)
		f.aim(Now(), true)
		events := f.tell.message(m.Message, sends, wasRunning, f.re.sent)
		events = append(events, f.tell.look(f.re)...)
		f.mu.Unlock()
		f.tellAll(events)
	case status == 0xFE:
		// active sensing: not a listener's (ADR-0002 part B)
	default:
		if event, ok := Decode(m.Message); ok && f.h.listen != nil {
			f.h.listen(Received{Port: f.in, Event: event, At: time.Now()})
		}
	}
}

// queue hands the re-maker's sends to the time thread, each a write at its time; with the following's lock held.
func (f *Following) queue(sends []Send) {
	// the pulses of the count: those sent while the transport runs, and those a Stop still owed, sent before it; a relay while stopped is
	// none of them
	counted := f.re.Running() || (len(sends) > 0 && sends[len(sends)-1].Message[0] == 0xFC)
	for _, s := range sends {
		f.h.sched.add(&job{at: s.At, id: f.in, index: -1, message: s.Message, follow: f, counted: counted})
	}
}

// aim puts the due job at the re-maker's next due time, or takes it off when nothing is due until a message comes; with the following's
// lock held. A message just heard may make a pulse due at once (the source's pulse that the line waited for); asked again by the time
// thread after a turn that sent nothing, it waits a quarter millisecond, so a due time Due does not yet find due is not asked back to back.
func (f *Following) aim(now int64, heard bool) {
	f.h.sched.cancel(f.due)
	next := f.re.NextDue()
	if next == 0 || f.stopped {
		return
	}
	if !heard {
		next = max(next, now+int64(250*time.Microsecond))
	}
	f.due.at = next
	f.h.sched.add(f.due)
}

// run is the time thread's turn at one of the following's jobs: the due job asks the re-maker, a write job writes.
func (f *Following) run(j *job) {
	if j == f.due {
		f.mu.Lock()
		if !f.stopped {
			now := Now()
			var late []Send
			for _, s := range f.re.Due(now) {
				// what is due now is written in this turn; only a pulse kept a quarter interval after the last waits for its time
				if s.At <= now {
					late = append(late, s)
				} else {
					f.queue([]Send{s})
				}
			}
			f.aim(now, false)
			events := f.tell.look(f.re)
			f.mu.Unlock()
			for _, s := range late {
				// Due sends pulses only while the transport runs
				f.write(s.Message, true, 0)
			}
			// told after the writes, which the time thread owes first
			f.tellAll(events)
			return
		}
		f.mu.Unlock()
		return
	}
	f.write(j.message, j.counted, j.at)
}

// write hands one message to every followed port's writer, each under its own lock (ADR-0006); a port closed is opened again first. The
// first port's writer tells when it wrote it (wroteAt): a pulse's time for the echo test, and for the report when it is one of the count
// (counted).
func (f *Following) write(message []byte, counted bool, due int64) {
	interval := int64(0)
	f.mu.Lock()
	if isPulse(message) {
		interval = int64(f.re.f.Interval())
	}
	// the ports as they are now: a listener may add one or take one off while it runs, and either replaces both lists whole, so these are
	// never changed under the loop
	ports, outs := f.ports, f.outs
	f.mu.Unlock()
	for i, o := range ports {
		o.mu.Lock()
		if o.output == nil {
			if output, err := f.h.driver.OpenOut(outs[i]); err == nil {
				f.h.attachLocked(outs[i], o, output)
			}
		}
		e := entry{msg: message, interval: interval, due: due}
		if i == 0 {
			e.follow, e.counted = f, counted
		}
		_ = f.h.handLocked(o, "", e)
		o.mu.Unlock()
	}
}

// wroteAt is told by the first port's writer when it wrote a message of this following's.
func (f *Following) wroteAt(at int64, message []byte, counted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recording {
		f.recordLocked(Recorded{Out: true, At: at, Message: fmt.Sprintf("%X", message)})
	}
	if !isPulse(message) {
		return
	}
	f.re.Wrote(at)
	if counted && len(f.written) < maxKept {
		f.written = append(f.written, at)
	}
}

// Stop ends the following: the input closed, nothing more queued, and Stop sent to the followed ports when the transport runs, so no
// follower is left running on a clock that ended.
func (f *Following) Stop() {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return
	}
	f.stopped = true
	f.h.sched.cancel(f.due)
	running := f.re.Running()
	input := f.input
	f.mu.Unlock()
	if input != nil {
		_ = input.Close()
	}
	f.h.mu.Lock()
	if f.h.ins[f.in] == input {
		delete(f.h.ins, f.in)
	}
	if f.h.follows[f.in] == f {
		delete(f.h.follows, f.in)
	}
	f.h.mu.Unlock()
	if running {
		f.write([]byte{0xFC}, false, 0)
	}
}

// FollowReport is what a following has done (part D's counts and part G's measures).
type FollowReport struct {
	State    string       `json:"state"`
	Tempo    float64      `json:"tempo"`
	SpreadMs float64      `json:"spreadMs"`
	Follower FollowCounts `json:"follower"`
	Remaker  RemakeCounts `json:"remaker"`
	Stamps   StampCounts  `json:"stamps"`
	// Returns counts the followed port coming back after it was gone (a cable pulled and put back), its input opened again
	Returns uint64 `json:"returns"`
	// Heard and Written are the source's pulses heard and the hub's written since the last Start, each counted on its own
	Heard   int `json:"heard"`
	Written int `json:"written"`
	// Compared is how many of the hub's pulses since the last Start were paired with the source's of the same place in the count;
	// Phase, how far after the source's each was written (negative: before); SourceSpacing and SentSpacing, each side's spacing off
	// its own median, every pulse: the source's own steadiness, and the hub's
	Compared      int    `json:"compared"`
	Phase         Spread `json:"phaseMs"`
	SourceSpacing Spread `json:"sourceSpacingMs"`
	SentSpacing   Spread `json:"sentSpacingMs"`
	// SourceRMS and SentRMS are each side's spacing, RMS off its own median, in milliseconds
	SourceRMS float64 `json:"sourceRmsMs"`
	SentRMS   float64 `json:"sentRmsMs"`
	// LongestSourceGapMs is the source's longest spacing since the last Start, its worst moment
	LongestSourceGapMs float64 `json:"longestSourceGapMs"`
	// DeliveryLag is how late the hub's callback took each source pulse after the system stamped it (part B's callback lag): the part of
	// the phase that is the system's and the process's delivery, not the hub's sending
	DeliveryLag Spread `json:"deliveryLagMs"`
	// Settled are the same from the second beat after Start on, once the follower has had its first beat to lock: the steady state
	// part G's criteria are about, apart from how it settled
	Settled Settled `json:"settled"`
}

// Settled is a following's phase and spacing from the second beat after Start on.
type Settled struct {
	Compared  int     `json:"compared"`
	Phase     Spread  `json:"phaseMs"`
	SourceRMS float64 `json:"sourceRmsMs"`
	SentRMS   float64 `json:"sentRmsMs"`
}

// Report is what the following has done so far.
func (f *Following) Report() FollowReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := FollowReport{
		State: f.re.f.State(), Tempo: f.re.f.Tempo(), SpreadMs: f.re.f.Spread() / 1e6, Follower: f.re.f.Counts(),
		Remaker: f.re.Counts(), Stamps: f.stamps, Returns: f.reopened,
	}
	n := min(len(f.source), len(f.written))
	r.Heard, r.Written, r.Compared = len(f.source), len(f.written), n
	phase := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		phase = append(phase, float64(f.written[i]-f.source[i])/1e6)
	}
	r.Phase = signedSpreadOf(phase)
	r.SourceSpacing, r.SourceRMS, r.LongestSourceGapMs = spacingOf(f.source)
	r.SentSpacing, r.SentRMS, _ = spacingOf(f.written)
	r.DeliveryLag = spreadOf(f.lags)
	if n > 24 {
		r.Settled.Compared = n - 24
		r.Settled.Phase = signedSpreadOf(phase[24:])
		_, r.Settled.SourceRMS, _ = spacingOf(f.source[24:n])
		_, r.Settled.SentRMS, _ = spacingOf(f.written[24:n])
	}
	return r
}

// signedSpreadOf is a spread of values that may be negative: its median and 99th percentile by size, and its largest by size, each
// with its sign.
func signedSpreadOf(values []float64) Spread {
	if len(values) == 0 {
		return Spread{}
	}
	s := spreadOf(values)
	largest := values[0]
	for _, v := range values {
		if math.Abs(v) > math.Abs(largest) {
			largest = v
		}
	}
	s.Max = largest
	return s
}

// spacingOf is a series' spacing off its own median, every step: its spread, its RMS and its longest step, in milliseconds.
func spacingOf(times []int64) (Spread, float64, float64) {
	if len(times) < 3 {
		return Spread{}, 0, 0
	}
	steps := make([]float64, 0, len(times)-1)
	longest := 0.0
	for i := 1; i < len(times); i++ {
		step := float64(times[i]-times[i-1]) / 1e6
		steps = append(steps, step)
		longest = math.Max(longest, step)
	}
	median := spreadOf(steps).Median
	off := make([]float64, len(steps))
	sum := 0.0
	for i, s := range steps {
		off[i] = math.Abs(s - median)
		sum += (s - median) * (s - median)
	}
	return spreadOf(off), math.Sqrt(sum / float64(len(steps))), longest
}
