//go:build windows

// Command machine-midi-clock-spike measures how close a 24 ppqn MIDI clock lands to its ideal grid on Windows, with one timing strategy
// after another, each pulse's time read with QueryPerformanceCounter. A throwaway spike for the hub's MIDI clock (ADR-0002, ADR-0003);
// README.md says how to run it. It changes nothing on the machine: every setting it makes is its own thread's or its own process's, and
// each is reverted after the strategy that uses it.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procQPC                    = kernel32.NewProc("QueryPerformanceCounter")
	procQPF                    = kernel32.NewProc("QueryPerformanceFrequency")
	procCreateWaitableTimerExW = kernel32.NewProc("CreateWaitableTimerExW")
	procSetWaitableTimer       = kernel32.NewProc("SetWaitableTimer")
	procSetThreadPriority      = kernel32.NewProc("SetThreadPriority")
	procGetThreadPriority      = kernel32.NewProc("GetThreadPriority")
	procSetProcessInformation  = kernel32.NewProc("SetProcessInformation")
	procQueryProcessCycleTime  = kernel32.NewProc("QueryProcessCycleTime")
	procGetSystemTimes         = kernel32.NewProc("GetSystemTimes")
	procGetSystemPowerStatus   = kernel32.NewProc("GetSystemPowerStatus")

	avrt                = windows.NewLazySystemDLL("avrt.dll")
	procAvSetMmThread   = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	procAvSetMmPriority = avrt.NewProc("AvSetMmThreadPriority")
	procAvRevertMm      = avrt.NewProc("AvRevertMmThreadCharacteristics")

	winmm                  = windows.NewLazySystemDLL("winmm.dll")
	procTimeBeginPeriod    = winmm.NewProc("timeBeginPeriod")
	procTimeEndPeriod      = winmm.NewProc("timeEndPeriod")
	procMidiOutGetNumDevs  = winmm.NewProc("midiOutGetNumDevs")
	procMidiOutGetDevCapsW = winmm.NewProc("midiOutGetDevCapsW")
	procMidiOutOpen        = winmm.NewProc("midiOutOpen")
	procMidiOutShortMsg    = winmm.NewProc("midiOutShortMsg")
	procMidiOutReset       = winmm.NewProc("midiOutReset")
	procMidiOutClose       = winmm.NewProc("midiOutClose")

	ntdll                      = windows.NewLazySystemDLL("ntdll.dll")
	procNtQueryTimerResolution = ntdll.NewProc("NtQueryTimerResolution")
)

const (
	createWaitableTimerHighResolution = 0x2
	timerAllAccess                    = 0x1F0003
	threadPriorityNormal              = 0
	threadPriorityTimeCritical        = 15
	avrtPriorityCritical              = 2
	processPowerThrottling            = 4 // PROCESS_INFORMATION_CLASS
	throttleExecutionSpeed            = 0x1
	throttleIgnoreTimerResolution     = 0x4
)

func qpc() int64 {
	var c int64
	procQPC.Call(uintptr(unsafe.Pointer(&c)))
	return c
}

var qpcFreq = func() int64 {
	var f int64
	procQPF.Call(uintptr(unsafe.Pointer(&f)))
	return f
}()

func ticksToMs(t float64) float64 { return t * 1000 / float64(qpcFreq) }

// A strategy is one way of waiting for the next pulse, with the thread and process settings it runs under.
type strategy struct {
	key, what  string
	wait       string // "sleep" (time.Sleep), "ticker" (time.Ticker), "timer" (an own high-resolution waitable timer)
	lock       bool   // runtime.LockOSThread
	mmcss      bool   // MMCSS "Pro Audio", AvSetMmThreadPriority critical
	critical   bool   // THREAD_PRIORITY_TIME_CRITICAL
	spin       bool   // wake early by -spin and spin on QPC to the pulse
	lead       bool   // aim the timer early by the median lateness of the last 32 pulses, and do not spin
	noThrottle bool   // the process opted out of EcoQoS and of having its timer resolution ignored
	period1    bool   // timeBeginPeriod(1) for the process
}

var strategies = []strategy{
	{key: "a", what: "time.Sleep to the next pulse", wait: "sleep"},
	{key: "b", what: "time.Ticker", wait: "ticker"},
	{key: "s", what: "a + spin to the pulse", wait: "sleep", spin: true},
	{key: "c", what: "own high-res waitable timer, locked thread", wait: "timer", lock: true},
	{key: "p", what: "c + TIME_CRITICAL", wait: "timer", lock: true, critical: true},
	{key: "q", what: "p + spin to the pulse", wait: "timer", lock: true, critical: true, spin: true},
	{key: "m", what: "c + MMCSS Pro Audio", wait: "timer", lock: true, mmcss: true},
	{key: "d", what: "c + MMCSS Pro Audio + TIME_CRITICAL", wait: "timer", lock: true, mmcss: true, critical: true},
	{key: "r", what: "d, aimed early by its recent median lateness", wait: "timer", lock: true, mmcss: true, critical: true, lead: true},
	{key: "e", what: "d + spin to the pulse", wait: "timer", lock: true, mmcss: true, critical: true, spin: true},
	{key: "f", what: "e + power throttling off", wait: "timer", lock: true, mmcss: true, critical: true, spin: true, noThrottle: true},
	{key: "g", what: "a + timeBeginPeriod(1)", wait: "sleep", period1: true},
	{key: "h", what: "f + timeBeginPeriod(1)", wait: "timer", lock: true, mmcss: true, critical: true, spin: true, noThrottle: true, period1: true},
}

// result holds every pulse of one strategy, across its segments.
type result struct {
	late      []float64 // ticks after the pulse's grid time (phase to the nearest slot for the ticker)
	intervals []float64 // ticks between consecutive pulses within a segment, minus the ideal interval
	maxGap    float64   // ticks, the longest time between two pulses
	missed    int       // pulses a ticker dropped
	drift     []float64 // per segment: mean lateness of its last 50 pulses minus its first 50, ticks
	cycles    float64   // the process's CPU cycles while the strategy ran (every thread, the Go runtime's included)
	wall      float64   // its wall time, seconds
	spun      float64   // ticks spent spinning
	idle      float64   // the machine's idle time across every logical processor while the strategy ran, 100 ns units
	total     float64   // its kernel and user time across every logical processor (kernel includes idle), 100 ns units
	call      []float64 // ticks spent in midiOutShortMsg
	gcs       uint32
	gcPause   uint64
	gcMax     uint64 // the longest stop-the-world pause seen, ns
	notes     map[string]bool
}

type options struct {
	bpm  float64
	seg  time.Duration
	spin time.Duration
	midi uintptr // an open MIDI output, or 0
}

// processCycles is the CPU cycles the process used so far: finer than GetProcessTimes, which charges whole clock ticks.
func processCycles() float64 {
	var cycles uint64
	procQueryProcessCycleTime.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&cycles)))
	return float64(cycles)
}

// systemTimes is the machine's idle and total processor time so far, summed over its logical processors.
func systemTimes() (idle, total float64) {
	var i, k, u windows.Filetime
	procGetSystemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u)))
	ft := func(f windows.Filetime) float64 { return float64(int64(f.HighDateTime)<<32 | int64(f.LowDateTime)) }
	return ft(i), ft(k) + ft(u)
}

func setThrottle(off bool) error {
	state := struct{ Version, ControlMask, StateMask uint32 }{Version: 1}
	if off {
		state.ControlMask = throttleExecutionSpeed | throttleIgnoreTimerResolution // both mechanisms chosen, both off
	}
	r, _, err := procSetProcessInformation.Call(uintptr(windows.CurrentProcess()), processPowerThrottling,
		uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if r == 0 {
		return err
	}
	return nil
}

// segment runs one strategy for opts.seg and adds its pulses to res.
func segment(s strategy, opts options, res *result) {
	interval := float64(qpcFreq) * 60 / (opts.bpm * 24)
	if s.noThrottle {
		if err := setThrottle(true); err != nil {
			res.notes["SetProcessInformation failed: "+err.Error()] = true
		}
		defer setThrottle(false)
	}
	if s.period1 {
		if r, _, _ := procTimeBeginPeriod.Call(1); r != 0 {
			res.notes[fmt.Sprintf("timeBeginPeriod returned %d", r)] = true
		}
		defer procTimeEndPeriod.Call(1)
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	cycles0, wall0 := processCycles(), qpc()
	idle0, total0 := systemTimes()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if s.lock {
			// the goroutine ends locked, so its thread ends with it and no priority set here outlives the strategy
			runtime.LockOSThread()
		}
		thread := windows.CurrentThread()
		var mm uintptr
		if s.mmcss {
			name, _ := windows.UTF16PtrFromString("Pro Audio")
			var index uint32
			h, _, err := procAvSetMmThread.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&index)))
			if h == 0 {
				res.notes["AvSetMmThreadCharacteristicsW failed: "+err.Error()] = true
			} else {
				mm = h
				procAvSetMmPriority.Call(h, avrtPriorityCritical)
				defer procAvRevertMm.Call(mm)
			}
		}
		if s.critical {
			if r, _, err := procSetThreadPriority.Call(uintptr(thread), threadPriorityTimeCritical); r == 0 {
				res.notes["SetThreadPriority failed: "+err.Error()] = true
			}
			defer procSetThreadPriority.Call(uintptr(thread), threadPriorityNormal)
		}
		if s.lock {
			p, _, _ := procGetThreadPriority.Call(uintptr(thread))
			res.notes[fmt.Sprintf("GetThreadPriority after set-up: %d", int32(p))] = true
		}
		var timer uintptr
		if s.wait == "timer" {
			h, _, err := procCreateWaitableTimerExW.Call(0, 0, createWaitableTimerHighResolution, timerAllAccess)
			if h == 0 {
				res.notes["CreateWaitableTimerExW failed: "+err.Error()] = true
				return
			}
			timer = h
			defer windows.CloseHandle(windows.Handle(h))
		}
		spin := float64(0)
		if s.spin {
			spin = float64(opts.spin) * float64(qpcFreq) / 1e9
		}
		end := qpc() + int64(float64(opts.seg)*float64(qpcFreq)/1e9)
		var times []int64
		var lates []float64

		if s.wait == "ticker" {
			ticker := time.NewTicker(time.Duration(float64(time.Minute) / (opts.bpm * 24)))
			start := qpc()
			lastSlot := int64(0)
			for {
				<-ticker.C
				now := qpc()
				if now > end {
					break
				}
				slot := int64(math.Round(float64(now-start) / interval))
				if lastSlot > 0 && slot-lastSlot > 1 {
					res.missed += int(slot - lastSlot - 1)
				}
				lastSlot = slot
				send(opts.midi, res)
				times = append(times, now)
				lates = append(lates, float64(now)-(float64(start)+float64(slot)*interval))
			}
			ticker.Stop()
		} else {
			start := float64(qpc())
			var recent [32]float64 // the last pulses' lateness, for the lead
			lead := 0.0
			for k := 1; ; k++ {
				target := start + float64(k)*interval
				if int64(target) > end {
					break
				}
				switch s.wait {
				case "sleep":
					if d := time.Duration((target - spin - float64(qpc())) * 1e9 / float64(qpcFreq)); d > 0 {
						time.Sleep(d)
					}
				case "timer":
					if rest := (target - spin - lead - float64(qpc())) * 1e7 / float64(qpcFreq); rest > 0 {
						due := -int64(rest) // relative, 100 ns units
						procSetWaitableTimer.Call(timer, uintptr(unsafe.Pointer(&due)), 0, 0, 0, 0)
						windows.WaitForSingleObject(windows.Handle(timer), windows.INFINITE)
					}
				}
				if s.spin {
					from := qpc()
					for float64(qpc()) < target {
					}
					res.spun += float64(qpc() - from)
				}
				now := qpc()
				send(opts.midi, res)
				times = append(times, now)
				lates = append(lates, float64(now)-target)
				if s.lead {
					// the lateness the timer would have had without the lead, its median over the last 32 pulses, at most 2 ms
					recent[k%len(recent)] = float64(now) - target + lead
					if k >= len(recent) {
						sorted := recent
						sort.Float64s(sorted[:])
						lead = math.Max(0, math.Min(sorted[len(sorted)/2], 0.002*float64(qpcFreq)))
					}
				}
			}
		}
		res.late = append(res.late, lates...)
		for i := 1; i < len(times); i++ {
			gap := float64(times[i] - times[i-1])
			res.intervals = append(res.intervals, gap-interval)
			if gap > res.maxGap {
				res.maxGap = gap
			}
		}
		if n := len(lates); n >= 100 {
			res.drift = append(res.drift, mean(lates[n-50:])-mean(lates[:50]))
		}
	}()
	<-done
	res.cycles += processCycles() - cycles0
	idle1, total1 := systemTimes()
	res.idle += idle1 - idle0
	res.total += total1 - total0
	res.wall += float64(qpc()-wall0) / float64(qpcFreq)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	res.gcs += after.NumGC - before.NumGC
	res.gcPause += after.PauseTotalNs - before.PauseTotalNs
	for n := before.NumGC + 1; n <= after.NumGC && after.NumGC-n < 256; n++ {
		res.gcMax = max(res.gcMax, after.PauseNs[(n+255)%256])
	}
}

// send writes one timing clock byte (F8) when a port is open, and keeps how long the call took.
func send(handle uintptr, res *result) {
	if handle == 0 {
		return
	}
	t0 := qpc()
	procMidiOutShortMsg.Call(handle, 0xF8)
	res.call = append(res.call, float64(qpc()-t0))
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	i := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

func sortedCopy(v []float64) []float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c
}

func rms(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x * x
	}
	return math.Sqrt(s / float64(len(v)))
}

func countAbove(v []float64, ms float64) int {
	n := 0
	for _, x := range v {
		if ticksToMs(x) > ms {
			n++
		}
	}
	return n
}

// probe reports the machine's timer resolution, the step of Go's monotonic clock and what a 1 ms time.Sleep takes.
func probe() {
	var coarsest, finest, current uint32
	procNtQueryTimerResolution.Call(uintptr(unsafe.Pointer(&coarsest)), uintptr(unsafe.Pointer(&finest)), uintptr(unsafe.Pointer(&current)))
	fmt.Printf("timer resolution (NtQueryTimerResolution): current %.3f ms, finest %.3f ms, coarsest %.3f ms\n",
		float64(current)/1e4, float64(finest)/1e4, float64(coarsest)/1e4)
	fmt.Printf("QueryPerformanceFrequency: %d Hz\n", qpcFreq)
	var power struct {
		ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag byte
		BatteryLifeTime, BatteryFullLifeTime                            uint32
	}
	procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&power)))
	fmt.Printf("power: AC line %d (1 on mains, 0 on battery), battery %d%%, saver %d\n", power.ACLineStatus,
		power.BatteryLifePercent, power.SystemStatusFlag)

	// the step of time.Now's monotonic reading: distinct values over 300 ms
	var steps []float64
	t0 := time.Now()
	prev := t0
	for time.Since(t0) < 300*time.Millisecond {
		now := time.Now()
		if d := now.Sub(prev); d > 0 {
			steps = append(steps, float64(d)/1e6)
			prev = now
		}
	}
	s := sortedCopy(steps)
	fmt.Printf("time.Now monotonic step: median %.3f ms, max %.3f ms (%d steps in 300 ms)\n", pct(s, 50), pct(s, 100), len(s))

	var sleeps []float64
	for i := 0; i < 200; i++ {
		a := qpc()
		time.Sleep(time.Millisecond)
		sleeps = append(sleeps, ticksToMs(float64(qpc()-a)))
	}
	s = sortedCopy(sleeps)
	fmt.Printf("time.Sleep(1ms) takes: median %.3f ms, p99 %.3f ms, max %.3f ms\n", pct(s, 50), pct(s, 99), pct(s, 100))
}

func openMidi(want string) (uintptr, string) {
	type outCaps struct {
		Mid, Pid      uint16
		DriverVersion uint32
		Name          [32]uint16
		Technology    uint16
		Voices        uint16
		Notes         uint16
		ChannelMask   uint16
		Support       uint32
	}
	n, _, _ := procMidiOutGetNumDevs.Call()
	for i := 0; i < int(n); i++ {
		var caps outCaps
		if r, _, _ := procMidiOutGetDevCapsW.Call(uintptr(i), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps)); r != 0 {
			continue
		}
		name := windows.UTF16ToString(caps.Name[:])
		if !strings.Contains(strings.ToLower(name), strings.ToLower(want)) {
			continue
		}
		var h uintptr
		t0 := time.Now()
		if r, _, _ := procMidiOutOpen.Call(uintptr(unsafe.Pointer(&h)), uintptr(i), 0, 0, 0); r != 0 {
			fmt.Fprintf(os.Stderr, "midiOutOpen %q failed (%d)\n", name, r)
			os.Exit(1)
		}
		fmt.Printf("MIDI out: %q opened in %s\n", name, time.Since(t0).Round(time.Millisecond))
		return h, name
	}
	fmt.Fprintf(os.Stderr, "no MIDI output named like %q\n", want)
	os.Exit(1)
	return 0, ""
}

func closeMidi(h uintptr) {
	for c := uintptr(0); c < 16; c++ {
		procMidiOutShortMsg.Call(h, 0xB0|c|123<<8) // All Notes Off on every channel
	}
	procMidiOutShortMsg.Call(h, 0xFC) // Stop
	procMidiOutReset.Call(h)
	procMidiOutClose.Call(h)
}

// burn spins n threads at normal priority for d: a CPU load the spike starts itself, as a separate process.
func burn(n int, d time.Duration) {
	var stop atomic.Bool
	for i := 0; i < n; i++ {
		go func() {
			x := 0
			for !stop.Load() {
				x++
			}
			_ = x
		}()
	}
	time.Sleep(d)
	stop.Store(true)
}

// gcLoad allocates and drops memory in this process until stopped, so the collector runs while the clock does: as fast as it can,
// or rate MB a second in 64 KiB pieces, keeping the last 64 MiB alive.
func gcLoad(stop *atomic.Bool, rate int) {
	keep := make([][]byte, 0, 1024)
	for !stop.Load() {
		pieces := 1
		if rate > 0 {
			time.Sleep(10 * time.Millisecond)
			pieces = rate * 16 / 100 // rate MB/s is rate*16 pieces a second, a hundredth of it every 10 ms
		}
		for i := 0; i < pieces; i++ {
			keep = append(keep, make([]byte, 64<<10))
			if len(keep) == cap(keep) {
				keep = keep[:0]
			}
		}
	}
}

func main() {
	bpm := flag.Float64("bpm", 120, "tempo; 24 pulses a quarter note")
	seg := flag.Duration("seg", 15*time.Second, "length of one strategy's segment")
	rounds := flag.Int("rounds", 3, "rounds; each runs every strategy once, so a change in the machine's load falls on all of them")
	which := flag.String("strategies", "a,b,s,c,p,q,m,d,r,e,f,g,h", "strategies to run, by key")
	spin := flag.Duration("spin", time.Millisecond, "how early the spinning strategies wake before the pulse")
	midi := flag.String("midi", "", "send each pulse (F8) to the MIDI output whose name contains this, e.g. \"GS Wavetable\"")
	gc := flag.Bool("gcload", false, "allocate in this process all the while, so the garbage collector runs")
	gcRate := flag.Int("gcrate", 0, "with -gcload, allocate this many MB a second instead of as fast as possible")
	burnN := flag.Int("burn", 0, "only burn CPU on this many threads, for -for, then exit (a load to run beside a measure)")
	burnFor := flag.Duration("for", 10*time.Minute, "how long -burn runs")
	flag.Parse()

	if *burnN > 0 {
		burn(*burnN, *burnFor)
		return
	}

	v := windows.RtlGetVersion()
	fmt.Printf("Windows %d.%d.%d, %s, GOMAXPROCS %d, NumCPU %d\n", v.MajorVersion, v.MinorVersion, v.BuildNumber,
		runtime.Version(), runtime.GOMAXPROCS(0), runtime.NumCPU())
	probe()
	opts := options{bpm: *bpm, seg: *seg, spin: *spin}
	if *midi != "" {
		h, _ := openMidi(*midi)
		opts.midi = h
		defer closeMidi(h)
	}
	var stop atomic.Bool
	if *gc {
		go gcLoad(&stop, *gcRate)
		defer stop.Store(true)
	}

	var chosen []strategy
	for _, key := range strings.Split(*which, ",") {
		for _, s := range strategies {
			if s.key == strings.TrimSpace(key) {
				chosen = append(chosen, s)
			}
		}
	}
	results := map[string]*result{}
	for _, s := range chosen {
		results[s.key] = &result{notes: map[string]bool{}}
	}
	fmt.Printf("%.0f BPM, a pulse every %.3f ms; %d rounds of %s per strategy; spin %s; midi %q; gcload %v\n",
		*bpm, 60000/(*bpm*24), *rounds, *seg, *spin, *midi, *gc)
	for r := 0; r < *rounds; r++ {
		for _, s := range chosen {
			fmt.Fprintf(os.Stderr, "round %d, %s\n", r+1, s.key)
			segment(s, opts, results[s.key])
		}
	}

	fmt.Println()
	fmt.Println("| | Strategy | Pulses | Median late | p95 | p99 | Max late | Longest gap | Interval jitter (RMS) | > 1 ms | > 5 ms | Missed | Drift | Spin a pulse | CPU | Machine busy |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, s := range chosen {
		res := results[s.key]
		l := sortedCopy(res.late)
		drift := 0.0
		for _, d := range res.drift {
			drift = math.Max(drift, math.Abs(ticksToMs(d)))
		}
		fmt.Printf("| %s | %s | %d | %.3f ms | %.3f ms | %.3f ms | %.2f ms | %.2f ms | %.3f ms | %d | %d | %d | %.3f ms | %.0f µs | %.0f Mcycles/s | %.0f%% |\n",
			s.key, s.what, len(l), ticksToMs(pct(l, 50)), ticksToMs(pct(l, 95)), ticksToMs(pct(l, 99)), ticksToMs(pct(l, 100)),
			ticksToMs(res.maxGap), ticksToMs(rms(res.intervals)), countAbove(res.late, 1), countAbove(res.late, 5), res.missed, drift,
			ticksToMs(res.spun)*1000/float64(max(len(l), 1)), res.cycles/res.wall/1e6, 100*(1-res.idle/res.total))
	}
	fmt.Println()
	for _, s := range chosen {
		res := results[s.key]
		if len(res.call) > 0 {
			c := sortedCopy(res.call)
			fmt.Printf("%s: midiOutShortMsg took median %.1f µs, p99 %.1f µs, max %.1f µs\n", s.key,
				ticksToMs(pct(c, 50))*1000, ticksToMs(pct(c, 99))*1000, ticksToMs(pct(c, 100))*1000)
		}
		if *gc {
			fmt.Printf("%s: %d collections, %.2f ms of stop-the-world pauses, the longest %.2f ms\n", s.key, res.gcs,
				float64(res.gcPause)/1e6, float64(res.gcMax)/1e6)
		}
		for note := range res.notes {
			fmt.Printf("%s: %s\n", s.key, note)
		}
	}
}
