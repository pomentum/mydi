//go:build windows

package mydi

import (
	"math"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The system's own clock and timer, and the thread's priority (ADR-0003 parts B and C): no CGo, each call by name.
var (
	timingKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procQPC                 = timingKernel32.NewProc("QueryPerformanceCounter")
	procQPF                 = timingKernel32.NewProc("QueryPerformanceFrequency")
	procCreateWaitableTimer = timingKernel32.NewProc("CreateWaitableTimerExW")
	procSetWaitableTimer    = timingKernel32.NewProc("SetWaitableTimer")
	procSetThreadPriority   = timingKernel32.NewProc("SetThreadPriority")
	procSetProcessInfo      = timingKernel32.NewProc("SetProcessInformation")
	avrt                    = windows.NewLazySystemDLL("avrt.dll")
	procAvSetMmThread       = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	procAvSetMmPriority     = avrt.NewProc("AvSetMmThreadPriority")
)

const (
	createWaitableTimerHighResolution = 0x2
	timerAllAccess                    = 0x1F0003
	threadPriorityTimeCritical        = 15
	avrtPriorityCritical              = 2
	processPowerThrottling            = 4
	throttleExecutionSpeed            = 0x1
	throttleIgnoreTimerResolution     = 0x4
	// never is a wait with nothing due: only a wake ends it
	never = math.MaxInt64
	// leadMax is the most the thread aims early; leadShift how slowly its estimate moves: a sixty-fourth of each difference
	leadMax   = 2_000_000
	leadShift = 6
)

var qpcFrequency = func() int64 {
	var f int64
	procQPF.Call(uintptr(unsafe.Pointer(&f)))
	if f <= 0 {
		f = 10_000_000
	}
	return f
}()

// Now is the timeline the time thread keeps, in nanoseconds: the system's performance counter, read directly, never Go's clock, which
// moves a system tick at a time on Windows (ADR-0003 part B).
func Now() int64 {
	var c int64
	_, _, _ = syscall.SyscallN(procQPC.Addr(), uintptr(unsafe.Pointer(&c)))
	return (c/qpcFrequency)*1_000_000_000 + (c%qpcFrequency)*1_000_000_000/qpcFrequency
}

var unthrottle sync.Once

// processClass is the process's priority class as the time thread set it, for the timing to report.
var processClass atomic.Value

// waiter is the time thread's own: its high-resolution timer, an event that wakes it early, and its lead. What its calls write into is
// its own too, and each call is made by its address, so the thread allocates nothing while it keeps time: an allocation during a
// collection can make it do the collector's work.
type waiter struct {
	timer, event windows.Handle
	lead         atomic.Int64
	priority     string
	qpc, due     int64
	handles      [2]windows.Handle
	qpcAt, setAt uintptr
}

// newWaiter is made on the time thread: its priority first, then its timer; the process asks not to be throttled, once.
func newWaiter() *waiter {
	w := &waiter{priority: "normal"}
	unthrottle.Do(func() {
		// this process's own setting only: its execution speed and its timer requests not throttled (ADR-0003 part C)
		state := struct{ Version, ControlMask, StateMask uint32 }{Version: 1, ControlMask: throttleExecutionSpeed | throttleIgnoreTimerResolution}
		procSetProcessInfo.Call(uintptr(windows.CurrentProcess()), processPowerThrottling, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
		// and its class above normal (ADR-0006 part F): a thread locked to its OS thread that comes back from a wait without its processor
		// gets one from the runtime's other threads, which run at the class's normal priority; with every core busy in other processes
		// they were starved, and the time thread waited up to 330 ms though its own priority was time-critical (2026-11-15)
		if windows.SetPriorityClass(windows.CurrentProcess(), windows.ABOVE_NORMAL_PRIORITY_CLASS) == nil {
			processClass.Store("above-normal")
		}
	})
	name, _ := windows.UTF16PtrFromString("Pro Audio")
	var index uint32
	if h, _, _ := procAvSetMmThread.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&index))); h != 0 {
		procAvSetMmPriority.Call(h, avrtPriorityCritical)
		w.priority = "mmcss"
	}
	if r, _, _ := procSetThreadPriority.Call(uintptr(windows.CurrentThread()), threadPriorityTimeCritical); r != 0 {
		if w.priority == "mmcss" {
			w.priority = "mmcss+time-critical"
		} else {
			w.priority = "time-critical"
		}
	}
	if h, _, _ := procCreateWaitableTimer.Call(0, 0, createWaitableTimerHighResolution, timerAllAccess); h != 0 {
		w.timer = windows.Handle(h)
	} else if h, _, _ := procCreateWaitableTimer.Call(0, 0, 0, timerAllAccess); h != 0 {
		// before Windows 10 1803 there is no high-resolution timer: the ordinary one, which the measure will show
		w.timer = windows.Handle(h)
	}
	w.event, _ = windows.CreateEvent(nil, 0, 0, nil)
	w.handles = [2]windows.Handle{w.event, w.timer}
	w.qpcAt, w.setAt = procQPC.Addr(), procSetWaitableTimer.Addr()
	return w
}

// now is Now on the time thread, allocating nothing.
func (w *waiter) now() int64 {
	_, _, _ = syscall.SyscallN(w.qpcAt, uintptr(unsafe.Pointer(&w.qpc)))
	c := w.qpc
	return (c/qpcFrequency)*1_000_000_000 + (c%qpcFrequency)*1_000_000_000/qpcFrequency
}

// waitUntil sleeps until a write is due less the lead, or until woken; it never spins.
func (w *waiter) waitUntil(at int64) {
	handles := w.handles[:1]
	if at != never && w.timer != 0 {
		rest := at - w.lead.Load() - w.now()
		if rest <= 0 {
			return
		}
		w.due = -(rest / 100) // relative, in 100 ns
		if w.due == 0 {
			w.due = -1
		}
		_, _, _ = syscall.SyscallN(w.setAt, uintptr(w.timer), uintptr(unsafe.Pointer(&w.due)), 0, 0, 0, 0)
		handles = w.handles[:2]
	}
	_, _ = windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
}

func (w *waiter) wake() { _ = windows.SetEvent(w.event) }
func (w *waiter) leadNs() int64 {
	if leadOff.Load() {
		return 0
	}
	return w.lead.Load()
}

// learn moves the lead a sixty-fourth of the way to the wake-up delay just seen (how late the thread woke against the time it aimed
// at), between 0 and 2 ms: slowly, and a stall counted as twice the cap at most, so one late wake does not pass its jitter on.
func (w *waiter) learn(late int64) {
	lead := w.lead.Load()
	delay := min(late+lead, 2*leadMax)
	lead += (delay - lead) / (1 << leadShift)
	lead = max(0, min(lead, leadMax))
	w.lead.Store(lead)
}

func (w *waiter) close() {
	if w.timer != 0 {
		_ = windows.CloseHandle(w.timer)
	}
	if w.event != 0 {
		_ = windows.CloseHandle(w.event)
	}
}
