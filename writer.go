package mydi

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// A writer per port (ADR-0006): every port's driver calls are its writer's, a thread of its own, so a driver that holds a write holds
// only its port; the time thread decides when, and hands each timed write a little ahead of its time (handAhead) with that time, which
// the writer waits for on its own timer: the handoff is made before the write is due, so its tail never reaches the write. The writer is a goroutine locked to an OS thread of its own at the time thread's priority, with the time
// thread's kind of waiter (its timer, its wake event), and a fixed ring of what it was handed, in order. It keeps the count of the clock
// it carries from what it is handed (Start, Continue, Song Position, pulses), so a port held past a beat is put back in its place with
// Stop, Song Position and Continue rather than a burst of the pulses it missed.

const (
	// ringSize holds about 20 s of a clock at 300 BPM with events beside it; a port held past it is taken for stuck (part D)
	ringSize = 4096
	// staleNote is how late a note-on may be written: past it, a wrong note, dropped and counted (part C)
	staleNote = int64(50 * time.Millisecond)
	// defaultStuckAfter is how long a driver's call may hold before its writer is abandoned (part D)
	defaultStuckAfter = int64(20 * time.Second)
	// defaultBeat is a beat where no clock says one: a held port past it resynchronises
	defaultBeat = int64(500 * time.Millisecond)
	// handAhead is how long before its time a timed write is handed to its writer: past the time thread's own wake-up tail, so the
	// writer has it before it is due
	handAhead = int64(2 * time.Millisecond)
	// handoffBuckets are the handoff histogram's: 10 µs each to 20 ms, the last one everything past it
	handoffBuckets = 2001
	handoffBucket  = int64(10 * time.Microsecond)
)

// stuckLimit is the limit set with SetStuckAfter, 0 for the default.
var stuckLimit atomic.Int64

// SetStuckAfter sets how long a driver's call may hold before its port's writer is given up; 0 sets the default, 20 s.
func SetStuckAfter(d time.Duration) { stuckLimit.Store(int64(d)) }

func stuckAfter() int64 {
	if d := stuckLimit.Load(); d > 0 {
		return d
	}
	return defaultStuckAfter
}

// entry is one write handed to a writer: the bytes, when they were handed, and what comes back from them.
type entry struct {
	msg    []byte
	handed int64
	// due is when it is to be written, the writer waiting for it; 0 for at once. tick marks the hub's own clock pulse, counted in the
	// timing (ADR-0003 part D)
	due  int64
	tick bool
	// interval is a pulse's clock interval, for its spacing and the beat; index its place in the count since Start, -1 for none
	interval int64
	index    int64
	// done is told how the write went; follow, the following told when it was written (counted, a pulse of its count)
	done    chan error
	follow  *Following
	counted bool
}

// PortTiming is one port's writes as its writer kept them (ADR-0006).
type PortTiming struct {
	ID string `json:"id"`
	// HeldMs is how long the driver's call in progress has taken so far, 0 when none is; LongestWriteMs the longest one took, SlowWrites
	// how many took more than 20 ms
	HeldMs         float64 `json:"heldMs"`
	LongestWriteMs float64 `json:"longestWriteMs"`
	SlowWrites     uint64  `json:"slowWrites"`
	// StaleNotes are note-ons dropped for coming too late; Resyncs the times a held port was put back in its place, Skipped the pulses
	// a resync left out; Collapsed the controller values a long hold left out for a later one of the same controller; Abandoned whether
	// a call that never returned made the port given up
	StaleNotes uint64 `json:"staleNotes"`
	Resyncs    uint64 `json:"resyncs"`
	Skipped    uint64 `json:"skipped"`
	Collapsed  uint64 `json:"collapsed"`
	Abandoned  uint64 `json:"abandoned"`
	// WakeJitterMs is how far the writer's wakes stray from its lead's estimate, a running average; with a landing set, Landed counts the
	// writes it landed, WokeLate those it woke for only after their time, and LandedLate those it woke early for and waited on that still
	// went out more than 0.3 ms late: the thread held off while it waited
	WakeJitterMs float64 `json:"wakeJitterMs"`
	Landed       uint64  `json:"landed"`
	WokeLate     uint64  `json:"wokeLate"`
	LandedLate   uint64  `json:"landedLate"`
	// StandDowns is how often the writer stood its landing down for landing late, StoodDown the timed writes it made so, and
	// StandingDown whether it stands down now (ADR-0008)
	StandDowns   uint64 `json:"standDowns"`
	StoodDown    uint64 `json:"stoodDown"`
	StandingDown bool   `json:"standingDown"`
	// WriteDelayMs is the port's steady delay from a pulse's time to its write call's return, a running median: latency, which a
	// receiver does not hear as time, and which the timing's late counts leave out (about 1 ms as a service in session 0, 2026-11-17)
	WriteDelayMs float64 `json:"writeDelayMs"`
}

// delayStepMax is the most the running median of a port's write delay moves a pulse: a hold of 100 ms moves it this, not 100 ms
const delayStepMax = int64(50 * time.Microsecond)

type writer struct {
	h      *Hub
	o      *out
	id     string
	output Output
	w      *waiter
	ready  chan struct{}
	exited chan struct{}
	// busy is when the driver's call in progress began, 0 when none is; abandoned, a call held past stuckAfter given up
	busy      atomic.Int64
	abandoned atomic.Bool

	mu      sync.Mutex
	ring    [ringSize]entry
	scratch [ringSize]entry
	head, n int
	quit    bool
	// delay is the running median of the time from a pulse's due time to its write call's return (PortTiming.WriteDelayMs)
	delay int64
	// the count of the clock this port carries, as handed: running, the next pulse's place, the place a resync waits for (-1 none)
	running  bool
	pos      int64
	resyncAt int64
	// lastPulse is when the last pulse was written, interval the last pulse's clock interval
	lastPulse int64
	interval  int64
	stats     PortTiming
	// jitter is WakeJitterMs in nanoseconds
	jitter float64
	// lander says whether this port's timed writes land on their time or stand down (ADR-0008), under mu
	lander *Lander
	// recalls counts the clock's recalls (recallTicks), under mu: a pulse taken out of the ring to be landed is dropped when one came
	// while it waited on the counter
	recalls uint64
}

// startWriter starts a port's writer on a thread of its own and waits until it can be woken.
func (h *Hub) startWriter(id string, o *out, output Output) *writer {
	w := &writer{h: h, o: o, id: id, output: output, ready: make(chan struct{}), exited: make(chan struct{}), resyncAt: -1,
		lander: NewLander(DefaultLander)}
	w.stats.ID = id
	h.writersMu.Lock()
	h.writers = append(h.writers, w)
	h.writersMu.Unlock()
	go w.loop()
	<-w.ready
	return w
}

// push hands one write to the writer, in order; false when its ring is full.
func (w *writer) push(e entry) bool {
	w.mu.Lock()
	if w.n == ringSize {
		w.mu.Unlock()
		return false
	}
	w.counted(&e)
	w.ring[(w.head+w.n)%ringSize] = e
	w.n++
	w.mu.Unlock()
	w.w.wake()
	return true
}

// counted keeps the count of the clock this port carries, from what it is handed, with the writer's lock held.
func (w *writer) counted(e *entry) {
	e.index = -1
	if len(e.msg) == 0 {
		return
	}
	switch e.msg[0] {
	case 0xFA:
		w.running, w.pos, w.resyncAt = true, 0, -1
	case 0xFB:
		w.running = true
	case 0xFC:
		w.running, w.resyncAt = false, -1
	case 0xF2:
		if len(e.msg) == 3 {
			w.pos = (int64(e.msg[1]) | int64(e.msg[2])<<7) * 6
		}
	case 0xF8:
		if w.running {
			e.index = w.pos
			w.pos++
		}
	}
}

// urgent puts these writes first, before anything waiting, and takes every note-on still waiting out: panic goes first (part C).
func (w *writer) urgent(messages [][]byte) {
	w.mu.Lock()
	kept := 0
	for i := 0; i < w.n; i++ {
		e := w.ring[(w.head+i)%ringSize]
		if isNoteOn(e.msg) {
			settledErr(e.done, ErrReleased)
			continue
		}
		w.scratch[kept] = e
		kept++
	}
	w.head, w.n = 0, 0
	now := Now()
	for _, m := range messages {
		if w.n == ringSize {
			break
		}
		w.ring[w.n] = entry{msg: m, handed: now, index: -1}
		w.n++
	}
	for i := 0; i < kept && w.n < ringSize; i++ {
		w.ring[w.n] = w.scratch[i]
		w.n++
	}
	w.mu.Unlock()
	w.w.wake()
}

// recallTicks takes out the hub's own clock pulses still waiting, handed ahead of their time (ADR-0007 part B), so a Stop handed next is
// followed by none: a receiver counts every pulse after Start, and one after Stop is one more than the listener meant. With the port's
// lock held, after its clock is gone, so no other is handed meanwhile.
func (w *writer) recallTicks() {
	w.mu.Lock()
	w.recalls++
	kept := 0
	for i := 0; i < w.n; i++ {
		e := w.ring[(w.head+i)%ringSize]
		if e.tick {
			if e.index >= 0 {
				w.pos--
			}
			settledErr(e.done, ErrReleased)
			continue
		}
		w.scratch[kept] = e
		kept++
	}
	w.head, w.n = 0, kept
	copy(w.ring[:kept], w.scratch[:kept])
	w.mu.Unlock()
}

// stop asks the writer to write what waits, close its port and end.
func (w *writer) stop() {
	w.mu.Lock()
	w.quit = true
	w.mu.Unlock()
	w.w.wake()
}

func isPulse(m []byte) bool  { return len(m) == 1 && m[0] == 0xF8 }
func isNoteOn(m []byte) bool { return len(m) == 3 && m[0]&0xF0 == 0x90 && m[2] > 0 }

// settledErr tells a waiting caller how its write went, once and without waiting.
func settledErr(done chan error, err error) {
	if done == nil {
		return
	}
	select {
	case done <- err:
	default:
	}
}

var continueMessage = []byte{0xFB}

func (w *writer) loop() {
	// locked for the writer's life: its priority and its timer are this thread's, never handed to other Go code (ADR-0003 part C)
	runtime.LockOSThread()
	w.w = newWaiter()
	close(w.ready)
	defer close(w.exited)
	defer w.w.close()
	// aimed is the time the writer last slept toward for a write handed ahead: only that teaches it its wake-up delay
	aimed := int64(never)
	for {
		w.mu.Lock()
		if w.n == 0 {
			quit := w.quit
			w.mu.Unlock()
			if quit {
				w.finish()
				return
			}
			w.w.waitUntil(never)
			continue
		}
		e := w.ring[w.head]
		// a write handed ahead waits for its time on the writer's own timer, aimed early by its lead and its landing (ADR-0006 part B),
		// none while the writer stands down (ADR-0008)
		land := landing.Load()
		standing := land > 0 && w.lander.Down()
		if standing {
			land = 0
		}
		if e.due > 0 && w.w.now() < e.due-w.w.leadNs()-land {
			w.mu.Unlock()
			aimed = e.due - land
			w.w.waitUntil(aimed)
			continue
		}
		if e.due > 0 && e.due-land == aimed {
			late := w.w.now() - aimed
			w.jitter += (math.Abs(float64(late-w.w.leadNs())) - w.jitter) / 16
			w.stats.WakeJitterMs = w.jitter / 1e6
			w.w.learn(late)
		}
		wokeBefore := e.due > 0 && w.w.now() < e.due
		aimed = never
		// a pulse never closer than a quarter of its interval to the last one written (ADR-0004 part D): what a short hold left is
		// caught up so, never in a burst
		if isPulse(e.msg) && e.interval > 0 && w.lastPulse != 0 {
			if until := w.lastPulse + e.interval/4; w.w.now() < until {
				w.mu.Unlock()
				w.w.waitUntil(until)
				continue
			}
		}
		w.ring[w.head] = entry{}
		w.head, w.n = (w.head+1)%ringSize, w.n-1
		now := w.w.now()
		resume := false
		switch {
		case isPulse(e.msg) && e.index >= 0 && w.resyncAt >= 0 && e.index < w.resyncAt:
			// a resync waits for its sixteenth: the pulses before it are left out
			w.stats.Skipped++
			w.mu.Unlock()
			settledErr(e.done, nil)
			continue
		case isPulse(e.msg) && e.index >= 0 && e.index == w.resyncAt:
			w.resyncAt, resume = -1, true
		case isNoteOn(e.msg) && now-e.handed > staleNote:
			w.stats.StaleNotes++
			w.mu.Unlock()
			settledErr(e.done, ErrReleased)
			continue
		}
		recalls := w.recalls
		w.mu.Unlock()
		// the last stretch, on the counter to the write's own time, when a landing is set (SetLanding)
		for land > 0 && e.due > 0 && w.w.now() < e.due {
		}
		// a clock's pulse its Stop recalled while it waited: not written, as one still in the ring is not
		if e.tick {
			w.mu.Lock()
			recalled := w.recalls != recalls
			if recalled && e.index >= 0 {
				w.pos--
			}
			w.mu.Unlock()
			if recalled {
				settledErr(e.done, ErrReleased)
				continue
			}
		}
		if land > 0 && e.due > 0 {
			late := w.w.now() - e.due
			w.mu.Lock()
			w.stats.Landed++
			switch {
			case !wokeBefore:
				w.stats.WokeLate++
			case late > int64(300*time.Microsecond):
				w.stats.LandedLate++
			}
			w.lander.Landed(!wokeBefore || late > int64(300*time.Microsecond))
			w.stats.StandDowns, w.stats.StandingDown = w.lander.StandDowns, w.lander.Down()
			w.mu.Unlock()
		} else if standing && e.due > 0 {
			w.mu.Lock()
			w.lander.Stood()
			w.stats.StoodDown, w.stats.StandingDown = w.lander.StoodDown, w.lander.Down()
			w.mu.Unlock()
		}
		if resume {
			if err := w.write(continueMessage, now); err != nil {
				w.failed(e, err)
				return
			}
		}
		// the handoff timed from when the write was to be made: handed, or its due time for one handed ahead
		if err := w.write(e.msg, max(e.handed, e.due)); err != nil {
			w.failed(e, err)
			return
		}
		if w.abandoned.Load() {
			return
		}
		at := w.w.now()
		if e.tick {
			// lateness past the port's steady delay: a constant latency is not late (WriteDelayMs)
			raw := at - e.due
			step := min(abs64(raw-w.delay)/8, delayStepMax)
			if raw < w.delay {
				step = -step
			}
			w.delay = max(0, w.delay+step)
			w.mu.Lock()
			w.stats.WriteDelayMs = float64(w.delay) / 1e6
			w.mu.Unlock()
			w.h.sched.counted(raw-w.delay, at, w.lastPulse)
		}
		if isPulse(e.msg) {
			w.lastPulse = at
			if e.interval > 0 {
				w.interval = e.interval
			}
		}
		settledErr(e.done, nil)
		if e.follow != nil {
			e.follow.wroteAt(at, e.msg, e.counted)
		}
	}
}

// write makes one driver call, timed: how long it held and, from when it was handed, the handoff (ADR-0006 part E). A call that held
// past a slow write releases what waited by its kind.
func (w *writer) write(message []byte, handed int64) error {
	began := w.w.now()
	w.busy.Store(began)
	err := w.output.Write(message)
	end := w.w.now()
	w.busy.Store(0)
	if w.abandoned.Load() {
		return nil
	}
	w.h.handoff(began - handed)
	took := end - began
	w.mu.Lock()
	if ms := float64(took) / 1e6; ms > w.stats.LongestWriteMs {
		w.stats.LongestWriteMs = ms
	}
	if took > int64(slowWrite) {
		w.stats.SlowWrites++
		w.released(took, end)
	}
	w.mu.Unlock()
	return err
}

// released decides what waited through a hold this long, ended now, with the writer's lock held (part C): with the clock more than a beat
// behind, a clock with a count is put back in its place (Stop, the Song Position of the next sixteenth the count reaches, Continue on it,
// the pulses before it left out), and a controller's waiting values collapsed to its latest; note-ons are dropped as they come, stale;
// nothing that ends a sound is dropped. How far behind is the oldest pulse still waiting, due that long ago, or the hold itself: several
// holds in a row, each under a beat, put the clock as far behind as one long one (a 732 ms gap of such holds went out as a catch-up burst
// in an hour's run, 2026-11-17, when only one call's hold was looked at).
func (w *writer) released(took, now int64) {
	beat := defaultBeat
	if w.interval > 0 {
		beat = 24 * w.interval
	}
	if after := resyncAfter.Load(); after > 0 {
		beat = after
	}
	behind := took
	for i := 0; i < w.n; i++ {
		if e := w.ring[(w.head+i)%ringSize]; isPulse(e.msg) && e.index >= 0 && e.due > 0 {
			behind = max(behind, now-e.due)
			break
		}
	}
	if behind <= beat {
		return
	}
	kept, pulses := 0, 0
	latest := map[[2]byte]int{}
	for i := 0; i < w.n; i++ {
		e := w.ring[(w.head+i)%ringSize]
		if isPulse(e.msg) && e.index >= 0 {
			pulses++
			settledErr(e.done, nil)
			continue
		}
		if len(e.msg) == 3 && e.msg[0]&0xF0 == 0xB0 && e.msg[1] < 120 {
			key := [2]byte{e.msg[0], e.msg[1]}
			if at, ok := latest[key]; ok {
				settledErr(w.scratch[at].done, nil)
				w.scratch[at] = e
				w.stats.Collapsed++
				continue
			}
			latest[key] = kept
		}
		w.scratch[kept] = e
		kept++
	}
	w.head, w.n = 0, 0
	if holdDrop.Load() {
		w.stats.Skipped += uint64(pulses)
	} else if w.running && (pulses > 0 || w.pos > 0) {
		interval := w.interval
		if interval <= 0 {
			interval = defaultBeat / 24
		}
		at := (w.pos + (resyncPause.Load()+interval-1)/interval + 5) / 6 * 6
		w.resyncAt = at
		w.stats.Resyncs++
		w.stats.Skipped += uint64(pulses)
		now := Now()
		w.ring[0] = entry{msg: []byte{0xFC}, handed: now, index: -1}
		w.ring[1] = entry{msg: []byte{0xF2, byte(at / 6 & 0x7F), byte(at / 6 >> 7 & 0x7F)}, handed: now, index: -1}
		w.n = 2
	}
	for i := 0; i < kept && w.n < ringSize; i++ {
		w.ring[w.n] = w.scratch[i]
		w.n++
	}
}

// failed ends the writer on a driver's error: the port closed, its clock stopped, what waited told it failed; opened again when used.
func (w *writer) failed(e entry, err error) {
	err = fmt.Errorf("%w: %v", ErrNoPort, err)
	settledErr(e.done, err)
	o := w.o
	o.mu.Lock()
	if o.writer == w {
		_ = w.output.Close()
		o.output, o.writer = nil, nil
		o.held = map[held]bool{}
		if o.clock != nil {
			o.clock = nil
			w.h.log("midi: the clock on %s stopped: %v", w.id, err)
		}
	}
	o.mu.Unlock()
	w.drop(ErrNoPort)
}

// drop tells everything waiting that it was not written.
func (w *writer) drop(err error) {
	w.mu.Lock()
	for i := 0; i < w.n; i++ {
		settledErr(w.ring[(w.head+i)%ringSize].done, err)
	}
	w.head, w.n = 0, 0
	w.mu.Unlock()
}

// finish closes the port once what waited is written.
func (w *writer) finish() {
	if !w.abandoned.Load() {
		_ = w.output.Close()
	}
}

// abandon gives up a writer whose driver's call has held past stuckAfter or whose ring is full, with the port's lock held: its thread
// is left in the call (a thread leaked, accepted), the port's handle taken for dead and never closed from another thread, the port gone
// until it is opened again (part D).
func (h *Hub) abandon(o *out, w *writer) {
	w.abandoned.Store(true)
	o.output, o.writer = nil, nil
	o.held = map[held]bool{}
	o.clock = nil
	w.mu.Lock()
	w.stats.Abandoned++
	w.mu.Unlock()
	w.drop(ErrNoPort)
	h.log("midi: %s held a write past %v: its writer is given up, the port opened again when used", w.id, time.Duration(stuckAfter()))
}

// handoff counts one handoff's time, from handed to the driver's call (part E): a fixed histogram, nothing allocated.
func (h *Hub) handoff(ns int64) {
	b := max(int64(0), ns) / handoffBucket
	if b >= handoffBuckets {
		b = handoffBuckets - 1
	}
	h.handoffs[b].Add(1)
}

// handoffSpread is the handoffs' median and 99th percentile, in milliseconds, from the histogram (each to its bucket's top).
func (h *Hub) handoffSpread() (median, p99 float64, n uint64) {
	for i := range h.handoffs {
		n += h.handoffs[i].Load()
	}
	if n == 0 {
		return 0, 0, 0
	}
	var seen uint64
	half, tail := (n+1)/2, n-n/100
	for i := range h.handoffs {
		seen += h.handoffs[i].Load()
		top := float64(int64(i+1)*handoffBucket) / 1e6
		if median == 0 && seen >= half {
			median = top
		}
		if seen >= tail {
			return median, top, n
		}
	}
	return median, float64(handoffBuckets*handoffBucket) / 1e6, n
}

// timing is the port's writes so far.
func (w *writer) timing() PortTiming {
	w.mu.Lock()
	t := w.stats
	w.mu.Unlock()
	if began := w.busy.Load(); began != 0 {
		t.HeldMs = float64(Now()-began) / 1e6
	}
	return t
}
