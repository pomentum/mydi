//go:build !windows

package mydi

import (
	"math"
	"sync/atomic"
	"time"
)

// never is a wait with nothing due: only a wake ends it.
const never = math.MaxInt64

// base anchors the timeline on Go's monotonic clock, which on Linux reads the system's own: the same thread with clock_nanosleep and
// SCHED_FIFO comes after its measure on a Pi (ADR-0003 part E).
var base = time.Now()

// Now is the timeline the time thread keeps, in nanoseconds.
func Now() int64 { return int64(time.Since(base)) }

// waiter waits on Go's timer here; its lead stays at 0 until this system's thread is measured.
type waiter struct {
	woken    chan struct{}
	timer    *time.Timer
	lead     atomic.Int64
	priority string
}

// processClass is the process's priority class: left to the system here until Linux is measured (ADR-0003 part E).
var processClass atomic.Value

func newWaiter() *waiter {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return &waiter{woken: make(chan struct{}, 1), timer: t, priority: "normal"}
}

func (w *waiter) waitUntil(at int64) {
	if at == never {
		<-w.woken
		return
	}
	rest := at - Now()
	if rest <= 0 {
		return
	}
	w.timer.Reset(time.Duration(rest))
	select {
	case <-w.timer.C:
	case <-w.woken:
		if !w.timer.Stop() {
			select {
			case <-w.timer.C:
			default:
			}
		}
	}
}

func (w *waiter) wake() {
	select {
	case w.woken <- struct{}{}:
	default:
	}
}

func (w *waiter) leadNs() int64 { return w.lead.Load() }
func (w *waiter) now() int64    { return Now() }
func (w *waiter) learn(int64)   {}
func (w *waiter) close()        { w.timer.Stop() }
