package src

import (
	"container/heap"
	"context"
	"fmt"
	"runtime"
	"runtime/trace"
	"sync"
	"sync/atomic"
	"time"
)

// Timing is how well the time thread kept time (ADR-0003 part D): what the program reports and the timing measure prints.
type Timing struct {
	// Pulses is how many clock pulses were written; Late1ms and Late5ms how many of them more than 1 and 5 ms after their place on the
	// grid, past their port's steady write delay (PortTiming.WriteDelayMs), which is latency, not time a receiver hears.
	Pulses  uint64 `json:"pulses"`
	Late1ms uint64 `json:"late1ms"`
	Late5ms uint64 `json:"late5ms"`
	// LongestGapMs is the longest time between two pulses of one clock; GridMoves how often a stall of more than four pulses moved a
	// grid on.
	LongestGapMs float64 `json:"longestGapMs"`
	GridMoves    uint64  `json:"gridMoves"`
	// LeadMs is how early the thread aims its timer now: its estimate of its own wake-up delay.
	LeadMs float64 `json:"leadMs"`
	// Priority is what the thread was given: "mmcss+time-critical", "time-critical", "mmcss" or "normal"; empty before it started.
	// ProcessClass is the process's priority class where it was set: "above-normal" (ADR-0006 part F)
	Priority     string `json:"priority,omitempty"`
	ProcessClass string `json:"processClass,omitempty"`
	// BlockedMs is how long the longest driver's call in progress has taken so far, 0 when none is: a driver whose write does not return
	// holds its port, and says so here while it lasts, not only once it is over (each port's own in Ports). LongestWriteMs is the longest
	// one write took on any port, SlowWrites how many took more than 20 ms.
	BlockedMs      float64 `json:"blockedMs"`
	LongestWriteMs float64 `json:"longestWriteMs"`
	SlowWrites     uint64  `json:"slowWrites"`
	// HandoffMedianMs and HandoffP99Ms are how long a write waited from the time thread's handing it to its writer's call, over Handoffs
	// writes (ADR-0006 part E); Ports each output's writes, writers given up included
	HandoffMedianMs float64      `json:"handoffMedianMs"`
	HandoffP99Ms    float64      `json:"handoffP99Ms"`
	Handoffs        uint64       `json:"handoffs"`
	Ports           []PortTiming `json:"ports,omitempty"`
	// Heard is the clock each input listened to brought in, between its Starts and Stops: a device that re-sends the clock it receives
	// (a synth's clock send) brings the hub's own back, so what a device received is counted beside what the hub wrote
	Heard []PortHeard `json:"heard,omitempty"`
}

// PortHeard is one input's clock: its pulses between Start (or Continue) and Stop, and how many of each it heard.
type PortHeard struct {
	ID     string `json:"id"`
	Pulses uint64 `json:"pulses"`
	Starts uint64 `json:"starts"`
	Stops  uint64 `json:"stops"`
}

// landing is how far before a write's time the thread wakes to wait the rest on the counter, 0 for none (SetLanding).
var landing atomic.Int64

// SetLanding sets the writers' landing: woken this much earlier than its lead says, a writer waits the last stretch to each write's own
// time on the system's counter rather than writing when it wakes, so a pulse lands on its time instead of up to a millisecond after; never
// longer than the landing and the lead. 0 writes on waking (ADR-0003 part C); the default is DefaultLanding, which each writer stands down
// from while it lands late (ADR-0008).
func SetLanding(d time.Duration) { landing.Store(int64(max(0, d))) }

// DefaultLanding is the landing each writer lands with until it stands down (ADR-0008 part A): the setting that held a synth's display
// still on an idle machine.
const DefaultLanding = time.Millisecond

func init() {
	SetLanding(DefaultLanding)
	SetLead(false)
}

// resyncAfter is how far behind, in nanoseconds, a held clock is put back in place rather than caught up, 0 a beat; resyncPause how
// long at least its Continue comes after its Song Position (SetResync).
var resyncAfter, resyncPause atomic.Int64

// SetResync sets how far behind, after a hold, a clock is resynchronised with Stop, a Song Position and Continue rather than caught up
// (ADR-0006 part C), 0 for a beat, and how long at least the Continue waits after the Song Position, for a device that relocates late:
// the Continue goes on the first sixteenth that far ahead.
func SetResync(after, pause time.Duration) {
	resyncAfter.Store(int64(max(0, after)))
	resyncPause.Store(int64(max(0, pause)))
}

// leadOff turns the lead off (SetLead).
var leadOff atomic.Bool

// SetLead turns the lead on or off: each thread's timer aimed early by its own estimate of its wake-up delay (ADR-0003 parts B and C),
// to be kept only if an A/B showed it helps. Off by default since 2026-11-17: with the landing on, ten minutes idle through a synth's
// clock send read 0.31 ms at the 99th percentile without it and 0.40 with it (the longer wait on the counter stood the landing down ten
// times), and two rounds with every core busy were even; kept as a switch for the measures on Linux and the Pi.
func SetLead(on bool) { leadOff.Store(!on) }

// slowWrite is a write that took long enough to matter to a clock: about a pulse at 120 BPM.
const slowWrite = 20 * time.Millisecond

// job is one timed write: an event's message, or a clock's next pulse, whose job is the clock's own and reused.
type job struct {
	at      int64 // on the timeline, in nanoseconds
	seq     uint64
	index   int // in the queue, or -1
	id      string
	o       *out
	owner   Owner
	message []byte
	clock   *clock
	// done, for an event sent for now, tells the caller waiting in Send how its write went
	done chan error
	// follow is a followed clock's job: its due job, or one of its writes (ADR-0004 part D); counted, a pulse of the count since Start,
	// decided when it was queued, since a Stop's owed pulses are written after the transport has stopped
	follow  *Following
	counted bool
}

// queue is the time thread's due writes, the soonest first, in the order they were queued at the same time.
type queue []*job

func (q queue) Len() int { return len(q) }
func (q queue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}
func (q queue) Swap(i, j int) { q[i], q[j] = q[j], q[i]; q[i].index = i; q[j].index = j }
func (q *queue) Push(x any)   { j := x.(*job); j.index = len(*q); *q = append(*q, j) }
func (q *queue) Pop() any {
	old := *q
	n := len(old)
	j := old[n-1]
	old[n-1] = nil
	j.index = -1
	*q = old[:n-1]
	return j
}

// scheduler is the hub's time thread (ADR-0003): one goroutine locked to its OS thread owns every timed write, on one queue ordered on
// the system's own clock, waiting on its own timer at a priority the system serves on time; it never spins.
type scheduler struct {
	h       *Hub
	mu      sync.Mutex
	queue   queue
	seq     uint64
	stopped bool
	w       *waiter
	timing  Timing
	start   sync.Once
	ready   chan struct{}
}

func newScheduler(h *Hub) *scheduler {
	return &scheduler{h: h, ready: make(chan struct{})}
}

// run starts the thread at its first use, and waits until it can be woken.
func (s *scheduler) run() {
	s.start.Do(func() {
		go s.loop()
		<-s.ready
	})
}

// add queues a timed write from any goroutine, and wakes the thread when it is now the first due.
func (s *scheduler) add(j *job) {
	s.run()
	if s.push(j) {
		s.w.wake()
	}
}

// push queues a write and says whether it is now the first due; the thread itself pushes a clock's next pulse without waking itself.
func (s *scheduler) push(j *job) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.seq++
	j.seq = s.seq
	heap.Push(&s.queue, j)
	return s.queue[0] == j
}

// cancel takes a write off the queue if it is still there.
func (s *scheduler) cancel(j *job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.index >= 0 && j.index < len(s.queue) && s.queue[j.index] == j {
		heap.Remove(&s.queue, j.index)
	}
}

// stop ends the thread; what is queued is dropped.
func (s *scheduler) stop() {
	s.mu.Lock()
	s.stopped = true
	for _, j := range s.queue {
		j.index = -1
	}
	s.queue = nil
	w := s.w
	s.mu.Unlock()
	if w != nil {
		w.wake()
	}
}

// Timing is what the thread has kept so far.
func (s *scheduler) Timing() Timing {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.timing
	if s.w != nil {
		t.LeadMs = float64(s.w.leadNs()) / 1e6
		t.Priority = s.w.priority
	}
	if class, ok := processClass.Load().(string); ok {
		t.ProcessClass = class
	}
	return t
}

func (s *scheduler) loop() {
	// locked for the thread's life: the priority and the timer are this thread's, and the thread ends with the goroutine, so its
	// priority is never handed to other Go code
	runtime.LockOSThread()
	w := newWaiter()
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
	close(s.ready)
	defer w.close()
	// aimed is the time the thread last slept toward: only a write it slept toward teaches it its wake-up delay
	aimed := int64(never)
	for {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		var next *job
		if len(s.queue) > 0 {
			next = s.queue[0]
		}
		// a write is due once the thread is within its lead of it, handed ahead of its time (ADR-0006): the timer was aimed that early
		ahead := int64(0)
		if next != nil && aheadOf(next) {
			ahead = handAhead
		}
		if next == nil || w.now() < next.at-w.leadNs()-ahead {
			aimed = never
			if next != nil {
				aimed = next.at - ahead
			}
			s.mu.Unlock()
			w.waitUntil(aimed)
			continue
		}
		heap.Pop(&s.queue)
		s.mu.Unlock()
		if next.at-ahead == aimed {
			w.learn(w.now() - aimed)
		}
		aimed = never
		// the thread decides and hands each write to its port's writer; it calls no driver (ADR-0006)
		switch {
		case next.follow != nil:
			next.follow.run(next)
		case next.clock != nil:
			s.pulse(next)
		default:
			s.h.play(next)
		}
	}
}

// aheadOf says whether a job's write is handed ahead of its time: a clock's pulse, an event, a following's queued write; not a
// following's own look at what is due, which decides from what has come by its time.
func aheadOf(j *job) bool {
	return j.follow == nil || j != j.follow.due
}

// counted is a clock pulse its port's writer wrote, late past its place, at this time, after the port's pulse before (0 for none): the
// timing's counts (ADR-0003 part D), and a late pulse marked in an execution trace when one is recorded.
func (s *scheduler) counted(late, at, last int64) {
	s.mu.Lock()
	s.timing.Pulses++
	if late > int64(time.Millisecond) {
		s.timing.Late1ms++
		if late > 5*int64(time.Millisecond) {
			s.timing.Late5ms++
		}
	}
	if last != 0 {
		if gap := float64(at-last) / 1e6; gap > s.timing.LongestGapMs {
			s.timing.LongestGapMs = gap
		}
	}
	s.mu.Unlock()
	if late > 5*int64(time.Millisecond) && trace.IsEnabled() {
		trace.Log(context.Background(), "midi", lateMessage(late))
	}
}

// lateMessages are a late pulse's marks by whole milliseconds, made once, so marking one in a trace allocates nothing on the time thread,
// which the collector would otherwise make wait (its own trace showed it).
var (
	lateMessages     []string
	lateMessagesOnce sync.Once
)

func lateMessage(late int64) string {
	lateMessagesOnce.Do(func() {
		lateMessages = make([]string, 2001)
		for ms := range lateMessages {
			lateMessages[ms] = fmt.Sprintf("late pulse %d ms", ms)
		}
	})
	return lateMessages[min(late/int64(time.Millisecond), int64(len(lateMessages)-1))]
}

// pulse writes a clock's pulse and queues its next on the grid (ADR-0002 part B): a short stall caught up, never two pulses closer
// than a quarter interval, a stall of more than four pulses moving the grid on.
func (s *scheduler) pulse(j *job) {
	c := j.clock
	c.mu.Lock()
	place := c.next
	c.mu.Unlock()
	// handed ahead of its place, the port's writer writing it there and counting it (counted)
	if !s.h.tick(c, place) {
		return
	}
	wrote := s.w.now()
	c.mu.Lock()
	interval := intervalOf(c.bpm)
	next := place + interval
	moved := wrote-next > 4*interval
	if moved {
		next = wrote + interval
	}
	c.next = next
	c.mu.Unlock()
	if moved {
		s.mu.Lock()
		s.timing.GridMoves++
		s.mu.Unlock()
	}
	j.at = max(next, wrote+interval/4)
	s.push(j)
}

// holdDrop, when set, drops a held clock's owed pulses past the limit instead of resynchronising it (SetHoldDrop).
var holdDrop atomic.Bool

// SetHoldDrop sets whether a clock held past the resync limit drops its owed pulses and goes on from its grid, with no Stop, Song Position
// or Continue, rather than resynchronising: nearest for a receiver that runs on its own clock in a gap and ignores Song Position, wrong
// for one that waits (it is left behind by the hold).
func SetHoldDrop(drop bool) { holdDrop.Store(drop) }

// ResyncDefaults answers what SetResync last set: how far behind a clock is resynchronised (0: a beat) and the least pause before its
// Continue.
func ResyncDefaults() (after, pause time.Duration) {
	return time.Duration(resyncAfter.Load()), time.Duration(resyncPause.Load())
}
