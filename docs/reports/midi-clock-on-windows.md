# A tight MIDI clock from the hub on Windows

**Question.** Musicians complain that MIDI clock from a computer, Ableton Live's above all and on Windows above all, wanders and stutters,
and sequencers that follow it are hard to trust. The program (Go, no CGo) makes MIDI clock itself (ADR-0002 part B). How do the
best tools keep time, why does Windows make it hard, what does Windows offer a process today, what does Go add or take away, and what
design makes the hub's clock tight enough to rely on, on Windows first and then on Linux and the Pi? **Date: 2026-11-13.**
**Evidence labels** as in [ADR-0000](../adrs/0000-record-architecture-decisions.md): **[V-web]** read on the cited page on 2026-11-13, through a fetch tool that
summarises pages, so quotations are as that tool returned them; **[V-web, search summary]** seen only in a search engine's summary of
the page, the page itself not read; **[V-code]** read in the source named (the Go toolchain's own source is Go 1.27.1's, read in the
`golang:1.27.1-bookworm` image); **[V-exp]** observed in the spike of this report (a throwaway spike), on the machine
and in the state its section names; **[Inf]** inference, not observed. **Method.** Public sources on Ableton Link and Live, Windows
timing, Windows' three MIDI stacks, Go's runtime on Windows, and how sequencers and receivers treat clock; then a spike that runs a 24
ppqn clock at 120 BPM with thirteen ways of waiting, each pulse timed with `QueryPerformanceCounter`, on one Windows 11 laptop. This
report builds on an earlier report on time and clocks (2026-11-13), which covered the clock's grid, Linux's real-time
thread and ALSA's sequencer, and does not repeat them.

The answer is short. **The complaints are real, and most of them come from where the clock is made, not from Windows alone.** A DAW
makes clock beside its audio, so its jitter grows with the audio buffer; on Windows its classic MIDI drivers take no timestamps, so the
program must wake on time to send each byte; and the USB interface adds its own. The program makes no sound, so it escapes the
first cause by construction. The second is the one that matters, and the spike shows that **on Windows it is a question of priority,
not of timers**. Go's `time.Sleep` and `time.Ticker` landed a pulse within about 0.5 ms most of the time, but on a busy machine their
99th percentile was 6 to 30 ms and their worst gaps 40 to 510 ms, and the ticker dropped pulses outright. A thread with its own
high-resolution timer and no priority was no better. The same thread raised with MMCSS "Pro Audio" and `THREAD_PRIORITY_TIME_CRITICAL`,
a thread's own settings that an ordinary user's process was granted, held a 99th percentile under 1 ms, a worst pulse of 2 to 11 ms and
an interval jitter of 0.23 to 0.43 ms RMS in six runs at 80 to 100% load, with no more CPU than the plain sleep; aimed early by its own
recent wake-up delay, its median fell from 0.38 ms to 2 µs, at some cost in its tail [V-exp]. Spinning on the counter for the last millisecond, the usual trick,
made the median perfect and the tail catastrophic: under full load a spinning thread stalled for up to 13 seconds [V-exp]. Two traps
are Go's own. On Windows, Go 1.27.1's clock is the interrupt time, which advances once per clock tick [V-code], so the clock's path
reads `QueryPerformanceCounter` itself. And the garbage collector stops every Go thread, the prioritised one included: at a moderate allocation rate the prioritised
thread kept its 99th percentile under 1 ms, but an allocation loop in the same process broke it outright (a 99th percentile of 0.8 s),
so the program keeps its garbage low and, where the tail must be guaranteed, the pulses leave Go's world [V-exp].

The design that follows is one **time thread** in the program: locked to its OS thread, its own high-resolution waitable timer, its own
clock (`QueryPerformanceCounter`), raised with MMCSS and `TIME_CRITICAL`, aiming early by its recent wake-up delay instead of spinning,
making the send itself, in a process that asks Windows not to throttle it and keeps its garbage low; every pulse's lateness counted and
shown. Where Windows MIDI Services is installed, it can go one step further and hand pulses to the operating system's own scheduler a
few tens of milliseconds ahead, outside Go altogether, which is what ALSA's sequencer offers on Linux and what Ableton relies on where a
driver takes timestamps. The same structure carries to Linux and the Pi with `clock_nanosleep` and `SCHED_FIFO`. The numbers to hold it
to are a 99th percentile under 1 ms at the program and under 1.5 ms on the wire, with no gap over one and a half intervals in an hour;
only a loopback rig on the DIN wire can say the second.

## Why a computer's MIDI clock wanders, Ableton's included

MIDI clock is a byte, `F8`, sent 24 times a quarter note; at 120 BPM one every 20.833 ms. A receiver that follows it reads tempo from
the spacing of those bytes, so every error in one pulse's time is an error in tempo. The complaints and the measurements name three
causes, and the sellers of sync hardware have measured the result.

- **The clock is tied to the audio buffer.** On an Ableton forum thread (Windows 7, Live 9.1.2, a USB audio interface, clock sent to a
  hardware sequencer) the clock at 135 BPM swung by up to ±5 BPM, audible as skipping, with a 15 ms ASIO buffer, and by ±0.2 BPM with a
  3 ms buffer: "The larger the ASIO buffer size, the more jitter on the computer MIDI clock"; a 2020 post in the same thread saw swings of
  3 to 7 BPM at 4,096 samples and 0.3 BPM at 512 or 1,024 ([V-web] [Ableton forum](https://forum.ableton.com/viewtopic.php?p=1617415)).
  Ableton's help says the buffers of Live and of the device offset the clock, corrected by hand with the MIDI Clock Sync Delay ([V-web,
  search summary] [Ableton help](https://help.ableton.com/hc/en-us/articles/209071149)); the manual says the delay shifts Live's time base against the sync
  signal and is set by ear, playing both sides until they line up ([V-web] [Live 12 manual, Synchronizing](https://www.ableton.com/en/live-manual/12/synchronizing-with-link-tempo-follower-and-midi)).
  Whether Live makes clock once per audio buffer is not on any page read [Inf: the forum's figures fit it]. Users elsewhere blame
  plugin delay compensation, which "live is not compensating the midi clock with", and a "higher priority of audio vs midi @ OS level",
  as opinions, not measurements ([V-web] [Elektronauts](https://www.elektronauts.com/t/digitakt-receiving-inconsistent-midi-clock-from-daw/48469?page=2)).
- **Windows' classic MIDI drivers take no timestamps.** Ableton's MIDI fact sheet: "Windows MME drivers cannot process timestamps", so
  "Live schedules outgoing events internally"; its own tests at about 50% CPU found most events within ±1 ms on Windows, with peaks of ±4
  ms on one interface, ±3 to 6 ms on a second and up to ±10 ms on a third; its rule is that "latency is preferable to jitter" ([V-web]
  [Live 12 manual, MIDI fact sheet](https://www.ableton.com/en/live-manual/12/midi-fact-sheet/)). So a program on Windows that sends
  through WinMM lives or dies by when the operating system wakes its thread [Inf].
- **The interface and its driver.** A clock plugin measured "sub-microsecond beat-to-beat jitter" through a DIN port, but "a continuous
  ±1.3ms deviation on more than half of the beats" over USB, where the host's own clock on the same connection did not show it; the
  review warns that "Your MIDI interface may still add its own internal jitter" ([V-web]
  [Sound On Sound, Omniclock](https://www.soundonsound.com/reviews/rapid-flow-omniclock)).
  Microsoft's own loopback tests found one rack interface on a vendor driver whose jitter was "quantized to almost exactly 1 ms", while
  class-compliant cables and interfaces sat at 23 to 120 µs ([V-web]
  [Windows MIDI Services, round-trip measurements](https://microsoft.github.io/MIDI/kb/usb-midi-device-round-trip-measurements/)).
  A USB-MIDI firmware author notes that "the USB MIDI spec only refers to bulk transfers", and measured about 2.7 ms of latency and 0.3 ms of jitter
  through an interface on Windows ([V-web] [midibox forum](https://forum.midibox.org/t/why-usb-bulk-and-not-interrupt-as-transfer-method/17883));
  bulk transfers share whatever is left of full-speed USB's 1 ms frames and promise no latency ([V-web, search summary]
  [Microchip, USB frames](https://developerhelp.microchip.com/xwiki/bin/view/applications/usb/how-it-works/frame/)).
- **The vendors' numbers.** E-RM measured Live 9 on Windows XP sending clock through a USB interface: cycle-to-cycle jitter from −38.4
  to +22.2 ms over 13,500 pulses, which "exceeds the lock-range of the slave PLL" ([V-web]
  [E-RM jitter report, 2014](https://www.e-rm.de/data/E-RM_report_Jitter_02_14_EN.pdf)). Sim'n Tonic timed 15 DAW and system pairs
  with a 250 MHz timer through one USB audio interface, 20 s each at a 128-sample buffer: a median of 990 µs RMS, 830 to 5,000 µs on
  Windows, 40 to 1,100 µs on macOS, and 2,100 µs for Ableton Live on Windows ([V-web]
  [Sim'n Tonic blog](https://simntonic.com/blogs/blog/how-much-does-u-sync-2-0-reduce-your-daw-s-midi-clock-jitter)). Both sell
  the cure; the figures are theirs, short runs on one interface.

None of the pages read measured the Windows scheduler itself as a cause. This case differs from a DAW's in one way that helps: the
program makes no sound, so its clock is tied to no audio buffer. What is left is the second and third cause: waking one thread at the
right time, and the path from the call to the wire [Inf].

## What Ableton Link does instead: share time, not pulses

Link does not send clock. Each app keeps its own timeline, a triple of `(beat, time, tempo)`; peers "adopt the last tempo value that
they've seen proposed", beats may differ only "by an integral offset", and apps with the same quantum "will be phase aligned" ([V-web]
[Link concepts](http://ableton.github.io/link)). Under it is a shared clock the code calls ghost time. A peer pings another every 50 ms;
each pong carries the other's ghost time and the times of the exchange, and the peer takes the offset as `ghostTime − (hostTime +
prevHostTime)/2`, collects more than 100 such values, and keeps their median as the offset, with a slope fixed at 1 ([V-web]
[Measurement.hpp](https://raw.githubusercontent.com/Ableton/link/master/include/ableton/link/Measurement.hpp),
[MeasurementService.hpp](https://raw.githubusercontent.com/Ableton/link/master/include/ableton/link/MeasurementService.hpp)). A session
is measured again every 30 s ([V-web] [Sessions.hpp](https://raw.githubusercontent.com/Ableton/link/master/include/ableton/link/Sessions.hpp)).
On Windows the host clock is `QueryPerformanceCounter`, in microseconds ([V-web]
[platforms/windows/Clock.hpp](https://raw.githubusercontent.com/Ableton/link/master/include/ableton/platforms/windows/Clock.hpp)). An app
asks Link where the beat is at the time its audio will reach the speakers: the README says to add the output latency to the system
time, and offers a `HostTimeFilter` that fits a line through the last 512 pairs of sample time and host time for an audio callback
without timestamps ([V-web] [Link README](https://raw.githubusercontent.com/Ableton/link/master/README.md),
[HostTimeFilter.hpp](https://raw.githubusercontent.com/Ableton/link/master/include/ableton/link/HostTimeFilter.hpp)). Link is GPLv2 or
later, or a proprietary licence, and its concepts page names no protocol document apart from the code (same pages).

Three lessons carry to the hub [Inf]. **Agree on time and compute beats from it; do not count ticks.** **Know where each event lands
on the output and schedule for that moment.** **Filter clocks with medians and regressions, not single samples.** The hub's hub already
keeps its grid from its start on a monotonic clock (ADR-0002); a listener still has no shared timeline to place an event on a beat, which
the earlier report's step 11 proposed.

## What Windows gives a process

### The clock tick, and who may change it

Windows wakes timers on its clock interrupt. The default period is 15.625 ms, and this machine reports a finest of 0.5 ms [V-exp:
`NtQueryTimerResolution` in the spike]. `timeBeginPeriod` asks for a shorter one. Before Windows 10 2004 that was global; since then
"this function no longer affects global timer resolution": a process that calls it gets the highest resolution any process asked for,
and one that does not is not guaranteed more than the default. On Windows 11, "if a window-owning process becomes fully occluded,
minimized, or otherwise invisible or inaudible", the request may be ignored. Microsoft warns that a higher resolution costs power and
"does not improve the accuracy of the high-resolution performance counter" ([V-web]
[timeBeginPeriod](https://learn.microsoft.com/en-us/windows/win32/api/timeapi/nf-timeapi-timebeginperiod)). A process may opt out of
having its requests ignored, for itself alone, with `SetProcessInformation(ProcessPowerThrottling)` and
`PROCESS_POWER_THROTTLING_IGNORE_TIMER_RESOLUTION` in the control mask and a zero state mask ([V-web]
[SetProcessInformation](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-setprocessinformation)).

On this desktop the tick was already 1 ms, because some other program had asked [V-exp]. **A machine is not a desktop**: a small Windows
box or a headless mini PC running only the program may sit at 15.625 ms [Inf]. Anything that leans on the tick, including Go's own clock
(below), must be measured on the hub's own kind of machine, not a developer's.

### Timers that do not need the tick

`CreateWaitableTimerExW` with `CREATE_WAITABLE_TIMER_HIGH_RESOLUTION` (Windows 10 1803 and later) makes a timer "for time-critical
situations when short expiration delays on the order of a few milliseconds are unacceptable" ([V-web]
[CreateWaitableTimerExW](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createwaitabletimerexw)). It needs no
`timeBeginPeriod` and changes nothing for other processes [Inf, from the two pages]. Its due time is either absolute, in UTC, or relative
(negative, in 100 ns units), and "If the system time is adjusted, the due time of any outstanding absolute timers is adjusted" ([V-web]
[SetWaitableTimer](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-setwaitabletimer)); a clock therefore arms
relative times computed from a monotonic counter, as the program's clock rules ask [Inf].

### Clocks to read

`QueryPerformanceCounter` is the clock to time with: "choose QueryPerformanceCounter" for resolution of 1 µs or better; it is monotonic,
independent of the time of day, includes sleep, and on a TSC costs "10s of CPU cycles to several hundred" with no system call ([V-web]
[Acquiring high-resolution time stamps](https://learn.microsoft.com/en-us/windows/win32/sysinfo/acquiring-high-resolution-time-stamps)).
Here it runs at 10 MHz [V-exp]. The interrupt-time count is something else: it "is incremented at each clock interrupt by the length of
a clock tick", and `timeBeginPeriod` "affects the resolution" of `QueryInterruptTime`; `QueryInterruptTimePrecise` is the fine one
([V-web] [QueryInterruptTime](https://learn.microsoft.com/en-us/windows/win32/api/realtimeapiset/nf-realtimeapiset-queryinterrupttime)).

### Priorities, MMCSS and quality of service

Within the normal priority class, `THREAD_PRIORITY_TIME_CRITICAL` gives a thread base priority 15, the highest without the real-time
class, which Microsoft says to "almost never use" ([V-web]
[Scheduling priorities](https://learn.microsoft.com/en-us/windows/win32/procthread/scheduling-priorities)), and warns that "A thread
with a base priority level above 11 interferes with the normal operation of the operating system" ([V-web]
[SetThreadPriority](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-setthreadpriority)): such a
thread must run briefly and block, never spin [Inf]. Windows boosts and lowers
the dynamic priority of threads between 0 and 15 to keep any from starving, and never boosts 16 to 31 ([V-web]
[Priority boosts](https://learn.microsoft.com/en-us/windows/win32/procthread/priority-boosts)). A thread that has been ready but starved for
"300 clock ticks (approximate 4 seconds on a 10ms clock)" is raised to 15 for a quantum, according to 2007 course slides on Windows 2000
to 2003 ([V-web] [HPI, advanced scheduling](https://osm.hpi.de/bs/2007/Slides/Unit04/4.5_AdvancedScheduling.pdf)); whether Windows 11
keeps those numbers was not found. So on a saturated machine, starved ordinary threads come up to the level of a `TIME_CRITICAL` one
[Inf].

The Multimedia Class Scheduler Service (MMCSS) is the audio way in: a thread calls `AvSetMmThreadCharacteristics`
with a task such as "Pro Audio" (the call may fail with `ERROR_PRIVILEGE_NOT_HELD`, [V-web] [AvSetMmThreadCharacteristicsW](https://learn.microsoft.com/en-us/windows/win32/api/avrt/nf-avrt-avsetmmthreadcharacteristicsw); the spike's ordinary user process got it, [V-exp]). Its "High" category runs at 23 to 26, "lower than only certain system-level tasks", and is "designed
for Pro Audio tasks"; threads that "have used their quota of CPU resource" drop to 1 to 7; `SystemResponsiveness` (default 20) keeps
20% of CPU for lower-priority work ([V-web]
[Multimedia Class Scheduler Service](https://learn.microsoft.com/en-us/windows/win32/procthread/multimedia-class-scheduler-service)).
The same page says the priority MMCSS gives also depends on "Whether the application is in the foreground", which a service never is.
On this machine the registry, read and not changed, holds the defaults: `SystemResponsiveness` 20 and "Pro Audio" in the High category
[V-exp]. MMCSS also tags its threads with the Deadline quality of service, "High performance to meet media deadlines". Background services, which
is what a program is, get Utility QoS, which "On battery, selects most efficient CPU frequency and schedules to efficient cores", and
a process can tag itself HighQoS by turning `PROCESS_POWER_THROTTLING_EXECUTION_SPEED` off ([V-web]
[Quality of Service](https://learn.microsoft.com/en-us/windows/win32/procthread/quality-of-service)). **All of these are the process's or
the thread's own settings.** The power plan, the MMCSS registry values, `SystemResponsiveness` and core parking are the machine's, and
mydi does not change them.

### What no process controls

A deferred procedure call (DPC) blocks "all threads ... from running on the same processor" while it runs, and Microsoft asks drivers
to keep DPCs under 100 µs and interrupt routines under 25 µs ([V-web]
[Guidelines for writing DPC routines](https://learn.microsoft.com/en-us/windows-hardware/drivers/kernel/guidelines-for-writing-dpc-routines),
[Measuring DPC/ISR time](https://learn.microsoft.com/en-us/windows-hardware/drivers/devtest/example-15--measuring-dpc-isr-time)). A
driver that breaks that rule (network, Wi-Fi, graphics and ACPI drivers are the usual suspects in forums, [V-web, search summary]) stalls
any thread, at any priority. LatencyMon measures exactly this and calls under about 100 µs fine ([V-web]
[LatencyMon](https://www.resplendence.com/latencymon)). A parked core "will not have any threads scheduled", and waking one costs time
([V-web] [Tuning servers for energy savings](https://devblogs.microsoft.com/sustainable-software/tuning-servers-for-energy-savings/)).
These are why a hub is tested on its own hardware, and why `doctor` should measure rather than guess [Inf].

## Windows' three MIDI stacks

| Stack | Scheduled output | What it means for the hub |
|---|---|---|
| WinMM (`midiOutShortMsg`), what the program uses today | None: the call sends now, and "might not return until the message has been sent to the output device" ([V-web] [midiOutShortMsg](https://learn.microsoft.com/en-us/windows/win32/api/mmeapi/nf-mmeapi-midioutshortmsg)). The old kernel path scheduled with "a timer resolution of one millisecond" ([V-web] [MIDI and DirectMusic components](https://learn.microsoft.com/en-us/windows-hardware/drivers/audio/midi-and-directmusic-components)) | The program's own thread decides the time; the call's own cost is part of the jitter |
| WinRT MIDI 1.0 (`Windows.Devices.Midi`) | None: a message's timestamp "has no meaning" when sent to a `MidiOutPort` ([V-web] [IMidiMessage.Timestamp](https://learn.microsoft.com/en-us/uwp/api/windows.devices.midi.imidimessage.timestamp)) | Nothing over WinMM |
| Windows MIDI Services (the new MIDI 1.0 and 2.0 stack, the `midisrv` service) | Yes, for apps on its SDK: each message carries a timestamp on the `QueryPerformanceCounter` timeline in 100 ns ticks; 0 sends now; at most 5 minutes ahead; the service's scheduler sends it, and a process's own timer settings "doesn’t make the MIDI service send scheduled messages more precisely" ([V-web] [MidiClock](https://microsoft.github.io/MIDI/sdk-reference/MidiClock), [overview](https://microsoft.github.io/MIDI/)) | The program could hand the next pulses to the service ahead of time, and its own stalls shorter than that lead would vanish |

Windows MIDI Services claims jitter "low microsecond range even without any compensation", depending on transport and device ([V-web]
[Windows MIDI Services](https://microsoft.github.io/MIDI/)), and warns that its scheduler "slows down when it holds thousands of
messages, so don’t schedule too many at once, or too far ahead" ([V-web]
[MidiEndpointConnection](https://microsoft.github.io/MIDI/sdk-reference/MidiEndpointConnection)): a lead of a few pulses suits it [Inf]. Its loopback tests, "not a benchmark", measured a 3-byte round trip of 1.18
to 1.51 ms over class-compliant USB interfaces (about 0.96 ms of it the DIN cable at 31,250 baud), jitter as interquartile range of 23 to
120 µs, and with all 24 logical processors at 100% the median moved about 200 µs and the worst message took 1,689 µs ([V-web]
[round-trip measurements](https://microsoft.github.io/MIDI/kb/usb-midi-device-round-trip-measurements/)). The WinMM and WinRT MIDI 1.0
APIs "have been repointed to the new Windows Service", which gives old apps multi-client ports but no scheduling: WinMM "could not be
reasonably extended to support" timestamps and scheduled messages (same overview page; [V-web]
[October 2024 update](https://devblogs.microsoft.com/windows-music-dev/windows-midi-services-oct-2024-update)).
`wdmaud2.drv` "turns everything else over to the new MIDI Service `midisrv.exe`" ([V-web]
[the 10 MIDI driver limit](https://devblogs.microsoft.com/windows-music-dev/windows-midi-services-and-the-10-midi-driver-limit/)). On
the machine measured (Windows 11) `midisrv` is running and `wdmaud2.drv` is present [V-exp: `sc query midisrv`], so a program's
WinMM calls already pass through the service [Inf].

Its status: general availability was announced on 2026-02-17, rolling out in phases to supported Windows 11 releases, with the SDK
runtime "In the coming months" as a separate package ([V-web]
[Windows Experience blog](https://blogs.windows.com/windowsexperience/2026/02/17/making-music-with-midi-just-got-a-real-boost-in-windows-11/));
the project's page still calls the API "currently in preview" ([V-web] [Windows MIDI Services](https://microsoft.github.io/MIDI/)). It is
WinRT. Beside it is a COM interface, `IMidiEndpointConnectionRaw`, whose `SendMidiMessagesRaw(timestamp, wordCount, words)` "schedules a
message the same way the WinRT senders do", obtained by `QueryInterface` on a `MidiEndpointConnection`, never by `CoCreateInstance`
([V-web] [COM extensions](https://microsoft.github.io/MIDI/sdk-reference/MidiEndpointConnection_COM-Extensions)). So a Go program needs
WinRT activation of the session and connection first, then plain COM calls; no flat C API was found [Inf: possible with
`combase.dll` through system calls, not tried]. Jitter Reduction timestamps of MIDI 2.0 are not used by the service: "Please do not
request or supply JR Timestamps" ([V-web]
[MIDI 2.0 implementation details](https://microsoft.github.io/MIDI/kb/midi2-implementation-details)). Microsoft is building its own
clock generator on the service, "the clock source rather than a box counting somebody else's pulses", for consumers in 2027; its page
gives no jitter figure and says accuracy "is limited by your MIDI device, by its connection to the PC" ([V-web]
[midiclock](https://microsoft.github.io/MIDI/tools/midiclock/)).

## Go on Windows

- **Timers.** Since Go 1.23 the runtime sleeps on a high-resolution waitable timer tied to its I/O completion port, which took timer
  resolution "from ~15.6ms to ~0.5ms" ([V-web] [Microsoft for Go Developers, 2024-10-01](https://devblogs.microsoft.com/go/high-resolution-timers-windows/)).
  In Go 1.27.1, when that timer exists, the runtime never calls `timeBeginPeriod` (`osRelax` returns at once), and `netpoll` arms the
  timer with `SetWaitableTimer` and `NtAssociateWaitCompletionPacket` for the delay it computes [V-code: `runtime/os_windows.go`,
  `runtime/netpoll_windows.go`].
- **Go's clock is the tick.** `runtime.nanotime` and the monotonic half of `time.Now` read the interrupt time from the shared user data
  page (`_INTERRUPT_TIME`) [V-code: `runtime/sys_windows_amd64.s`, `runtime/time_windows_amd64.s`], which advances once per clock
  interrupt (above). The runtime computes every timer's delay from it, so a timer's error can include up to one tick of that clock's
  lag, and the hub's `time.Now`, `time.Until` and `next.Add(interval)` are quantised to the tick [Inf, from the code and the page]. The
  probe saw `time.Now` advance in steps of about 0.6 to 1 ms here, with the tick at 1 ms [V-exp]. Go issue 67066, "use
  QueryPerformanceCounter in time.Now on Windows", was closed on 2026-10-16 with a change to `QueryInterruptTimePrecise` for the monotonic
  reading, its author measuring `nanotime` "from about 4 ns to 51 ns"; timer wake-ups are unchanged ([V-web]
  [golang/go#67066](https://github.com/golang/go/issues/67066)). It is not in Go 1.27.1 [V-code]. Until the hub's Go has it, the clock
  path should read `QueryPerformanceCounter` itself [Inf].
- **A goroutine is not a thread.** `runtime.LockOSThread` wires a goroutine to its thread, and "If the calling goroutine exits without
  unlocking the thread, the thread will be terminated" [V-code: `runtime/proc.go`], which is how a priority set on that thread is kept
  from leaking to other goroutines. A Go timer, though, fires on whatever thread runs the scheduler and then wakes the goroutine, so a
  priority on the locked thread does not speed the wake-up; the thread must wait on its own kernel timer [Inf; the earlier report's Linux
  reasoning, now measured on Windows below].
- **Preemption and the collector.** The runtime's monitor preempts a goroutine that has held its processor for 10 ms
  (`forcePreemptNS`), on Windows by `SuspendThread` and `GetThreadContext` from the monitor's own thread [V-code: `runtime/proc.go`,
  `retake`; `runtime/os_windows.go`, `preemptM`]. A suspended thread is not ready, and "the scheduler does not allocate any processor
  time to them, regardless of their priority" ([V-web] [Context switches](https://learn.microsoft.com/en-us/windows/win32/procthread/context-switches)),
  so a high-priority thread suspended by an ordinary one waits for the ordinary one to run again [Inf]. The collector takes 25% of the
  CPU while marking, makes allocating goroutines assist, and stops the world briefly at phase changes, with pauses "dominated by the time
  it takes to stop running goroutines" ([V-web] [Go GC guide](https://go.dev/doc/gc-guide)). A locked thread blocked in a system call
  is not stopped by that, but cannot run Go code again until the world restarts [Inf, from the guide and the runtime's system-call
  return path].
- **No CGo needed.** Every call the spike makes (`CreateWaitableTimerExW`, `SetWaitableTimer`, `WaitForSingleObject`,
  `SetThreadPriority`, `AvSetMmThreadCharacteristicsW`, `SetProcessInformation`, `QueryPerformanceCounter`, `midiOutShortMsg`) goes
  through `golang.org/x/sys/windows`' lazy DLLs [V-code: the spike].

## How serious sequencers and receivers keep time

- **Schedule ahead, with timestamps.** Live schedules MME output itself and hands timestamps to drivers that take them ([V-web] fact
  sheet above). Web Audio's standard pattern is a coarse timer, every 25 ms, that schedules notes up to 100 ms ahead on the audio clock;
  a long lookahead makes tempo changes slow, a short one lets jitter back in ([V-web]
  [A tale of two clocks](https://web.dev/articles/audio-scheduling)). Wessel and Wright: latency variation "should not exceed 1 ms", and
  the cure is "time tags" or signals locked to the audio stream ([V-web]
  [Problems and Prospects for Intimate Musical Control of Computers, 2001](https://arxiv.org/pdf/2010.01570)). ALSA's sequencer
  (earlier report) and Windows MIDI Services' scheduler are the operating systems' own versions of the same idea.
- **Spin for the last fraction.** No public sequencer source read here documents a spin-wait; the spike measures what it buys.
- **Receivers smooth.** Renoise's clock follower has a Smoothing setting, because "MIDI messages often only have very rough timing"
  ([V-web] [Renoise, MIDI clock](https://tutorials.renoise.com/wiki/MIDI_Clock)). E-RM describes a receiving PLL whose lock range large
  cycle-to-cycle jitter exceeds (report above). No hardware maker's manual read states how much jitter its clock input tolerates. Sync
  boxes such as E-RM's multiclock drive clock from a sample-accurate audio stream instead, to "±1 Sample Jitter with Audio Sync at 48kHz"
  ([V-web] [E-RM multiclock](https://e-rm.de/multiclock/)).
- **What people hear.** Wessel and Wright put latency at 10 ms and its variation at 1 ms (above). Players of a virtual Theremin noticed
  latency between 20 and 30 ms ([V-web]
  [Mäki-Patola and Hämäläinen](https://users.aalto.fi/~hamalap5/publications/icmcarticlefinal10.pdf)). A just-noticeable difference of
  about 6 ms in a rhythm's intervals is reported for Friberg and Sundberg ([V-web, search summary]
  [JASA 98(5), 1995](https://zendy.io/pdf-viewer/10.1121/1.413218)). So **a clock whose pulses vary by under 1 ms is as good as the wire
  allows, and one whose gaps reach 5 to 10 ms is heard as a tempo that moves** [Inf].

## The measurement

### The machine, as it was

[V-exp throughout this section] One laptop: an 8-core, 16-thread mobile processor, Windows 11, the Balanced power plan, on mains
power. `QueryPerformanceCounter` at 10 MHz; the system timer at 1.000 ms (finest 0.5 ms, default
15.625 ms), raised by some other program, not by the spike. It was a busy developer machine: a container engine's virtual machine with
many containers, endpoint protection, chat and browser processes, and other builds and tests. **No idle state was
available**: Windows' own processor counters read 80 to 100% busy before the spike started, and the spike's per-strategy reading of the
machine (`GetSystemTimes`) agrees. The spike, built in `golang:1.27.1-bookworm` with `CGO_ENABLED=0`, ran as an ordinary user process
in a console, changing nothing on the machine; one setting per strategy, its own and reverted. Every strategy ran in three rounds of 15
s (pass 1: 20 s), interleaved, about 2,157 pulses each. Strategies s, q and m were added after pass 1, and r for runs E to G.

| Run | Load | Strategies |
|---|---|---|
| Pass 1 | As found, near 100% busy, its heaviest moment of the day | a, b, c, p, d, e, f, g, h |
| A | As found, 80 to 88% busy | all but r |
| B | As found plus the spike's own 16 spinning threads at normal priority: 100% busy | all but r |
| C | As B, with Go's asynchronous preemption off (`GODEBUG=asyncpreemptoff=1`) | p, q, d, e, f, h |
| D | As B, again, preemption on, straight after C | the same |
| E | As found, with the collector kept busy in the same process (`-gcload`) | a, c, p, d, r, e |
| F | As found, each pulse sent to the Microsoft GS Wavetable Synth through `midiOutShortMsg` | a, d, r, e, h |
| G | As found, a repeat of every strategy at the end | all |
| E2 | As found, the collector at a moderate rate (`-gcload -gcrate 20`, then `100`: MB a second, 64 MiB kept alive) | a, d, r |

The strategies, each a way of waiting for the next pulse:

| Key | How it waits |
|---|---|
| a | `time.Sleep` until the pulse, on an ordinary goroutine |
| b | `time.Ticker` |
| s | a, waking 1 ms early and spinning on the counter |
| c | its own high-resolution waitable timer, on a locked OS thread |
| p | c with `THREAD_PRIORITY_TIME_CRITICAL` |
| q | p, waking 1 ms early and spinning |
| m | c with MMCSS "Pro Audio" at critical |
| d | c with MMCSS and `TIME_CRITICAL` |
| r | d, its timer aimed early by the median lateness of its last 32 pulses |
| e | d, waking 1 ms early and spinning |
| f | e with the process opted out of power throttling |
| g | a with `timeBeginPeriod(1)` |
| h | f with `timeBeginPeriod(1)` |

### The results

Each cell is median lateness / 99th percentile / maximum lateness / longest gap between two pulses / RMS of each interval's error, in
milliseconds. A perfect clock reads 0 / 0 / 0 / 20.8 / 0.

Runs as found and under CPU load:

| | Pass 1 | A | B | G |
|---|---|---|---|---|
| a | 0.54 / 32.42 / 208.0 / 208.7 / 5.92 | 0.46 / 6.28 / 19.2 / 39.9 / 1.51 | 5.36 / 29.69 / 71.1 / 82.5 / 8.54 | 0.49 / 18.64 / 43.3 / 60.0 / 3.44 |
| b | 0.46 / 9.41 / 10.4 / 88.1 / 7.75 | 0.20 / 8.08 / 10.4 / 510.5 / 12.15 | 2.45 / 10.14 / 10.4 / 167.8 / 13.94 | 0.44 / 8.56 / 10.4 / 85.9 / 4.49 |
| s |  | 0.00 / 11.19 / 33.9 / 51.4 / 2.67 | 7.72 / 436.43 / 873.7 / 885.3 / 26.39 | 0.04 / 45.12 / 204.4 / 215.2 / 8.38 |
| c | 0.46 / 6.76 / 37.0 / 57.3 / 2.00 | 0.51 / 93.02 / 451.3 / 417.8 / 10.43 | 11.51 / 69.53 / 121.0 / 133.4 / 18.42 | 0.51 / 7.28 / 20.5 / 41.1 / 1.80 |
| p | 0.39 / 18.14 / 96.0 / 116.5 / 4.44 | 0.37 / 0.82 / 7.9 / 28.2 / 0.31 | 0.34 / 0.65 / 18.9 / 39.4 / 0.68 | 0.39 / 1.05 / 7.3 / 27.9 / 0.44 |
| q |  | 0.00 / 1.75 / 21.3 / 42.2 / 0.85 | 0.00 / 2867.65 / 3305.1 / 3326.0 / 95.94 | 0.00 / 125.48 / 551.2 / 572.1 / 13.47 |
| m |  | 0.36 / 0.75 / 1.2 / 21.9 / 0.23 | 0.33 / 0.72 / 27.9 / 48.2 / 0.96 | 0.37 / 0.81 / 9.5 / 29.8 / 0.40 |
| d | 0.39 / 5.57 / 85.4 / 105.7 / 3.01 | 0.38 / 0.97 / 3.5 / 23.9 / 0.28 | 0.34 / 0.70 / 2.0 / 22.5 / 0.24 | 0.38 / 0.72 / 1.9 / 22.2 / 0.23 |
| r |  |  |  | 0.00 / 0.59 / 3.9 / 24.6 / 0.29 |
| e | 0.00 / 5063.95 / 5647.2 / 5668.1 / 105.88 | 0.00 / 0.86 / 14.1 / 35.0 / 0.68 | 0.00 / 12817.32 / 13254.8 / 13275.6 / 287.06 | 0.00 / 20.16 / 320.1 / 335.9 / 7.70 |
| f | 0.00 / 53.01 / 519.3 / 539.4 / 11.90 | 0.00 / 1.08 / 15.0 / 35.8 / 0.80 | 0.00 / 2280.51 / 2718.0 / 2738.8 / 73.87 | 0.00 / 2.68 / 17.0 / 37.9 / 1.01 |
| g | 0.48 / 144.28 / 303.4 / 320.4 / 15.70 | 0.37 / 6.00 / 38.3 / 56.8 / 1.53 | 4.03 / 165.55 / 383.6 / 403.9 / 23.57 | 0.42 / 9.34 / 48.3 / 54.2 / 2.29 |
| h | 0.00 / 49.03 / 241.1 / 262.0 / 6.96 | 0.00 / 0.51 / 2.7 / 23.5 / 0.16 | 0.00 / 30.13 / 132.5 / 153.3 / 6.32 | 0.00 / 5.66 / 35.3 / 56.1 / 1.98 |

Runs that test one cause each:

| | C | D | E | F |
|---|---|---|---|---|
| a |  |  | 0.70 / 158.90 / 350.4 / 371.1 / 17.36 | 0.78 / 4452.34 / 4533.3 / 3266.8 / 71.67 |
| c |  |  | 1.13 / 141.39 / 346.6 / 349.7 / 15.08 |  |
| p | 0.33 / 0.71 / 58.5 / 78.7 / 1.53 | 0.34 / 1.04 / 22.9 / 43.2 / 1.47 | 0.39 / 1344.67 / 1782.1 / 1802.6 / 40.69 |  |
| q | 0.00 / 144.11 / 335.1 / 355.9 / 13.89 | 0.00 / 1806.29 / 2243.5 / 2264.4 / 50.18 |  |  |
| d | 0.32 / 0.78 / 4.0 / 24.6 / 0.28 | 0.33 / 0.67 / 11.3 / 31.9 / 0.43 | 0.42 / 824.38 / 1136.9 / 1157.6 / 33.01 | 0.38 / 0.98 / 3.9 / 24.3 / 0.31 |
| r |  |  | 0.00 / 97.39 / 368.2 / 389.4 / 11.07 | 0.00 / 0.83 / 12.4 / 33.2 / 0.47 |
| e | 0.00 / 42.41 / 141.8 / 162.6 / 6.62 | 0.00 / 2577.72 / 3015.2 / 3036.0 / 65.95 | 0.00 / 83.93 / 382.9 / 403.8 / 11.02 | 0.00 / 266.31 / 540.4 / 561.2 / 15.90 |
| f | 0.00 / 180.30 / 451.1 / 471.9 / 15.55 | 0.00 / 33.01 / 143.4 / 164.2 / 6.88 |  |  |
| h | 0.00 / 101.78 / 268.4 / 289.3 / 11.48 | 0.00 / 44.25 / 357.1 / 377.9 / 10.02 |  | 0.00 / 2749.72 / 3184.2 / 3205.1 / 73.11 |

The collector at a moderate rate (E2):

| | E2 at 20 MB/s | E2 at 100 MB/s |
|---|---|---|
| a | 0.60 / 572.88 / 1010.4 / 1030.8 / 25.18 | 0.48 / 13.78 / 52.6 / 72.7 / 3.58 |
| d | 0.37 / 0.91 / 7.5 / 28.0 / 0.53 | 0.37 / 0.95 / 3.6 / 24.1 / 0.31 |
| r | 0.00 / 1.21 / 20.5 / 41.2 / 1.06 | 0.00 / 1.00 / 9.5 / 30.2 / 0.51 |

The full tables, with the 95th percentile, the counts over 1 and 5 ms, the CPU and the machine's load, are in the spike's
README.

### What they say

- **Go's own waiting is fine on a quiet moment and not under load.** Sleep (a) and the ticker (b) put the median pulse 0.2 to 0.5 ms late
  with headroom, but their 99th percentile was 6 to 19 ms in runs A and G and 10 to 30 ms in B, and their gaps reached 40 to 510 ms.
  **The ticker drops pulses** when its reader is late (127 in A, 597 in B, 76 in G): a clock must never be a `time.Ticker`. `timeBeginPeriod(1)` (g)
  did nothing for Go's sleep, as the runtime's own high-resolution timer already ignores the tick.
- **The timer is not the problem; the thread's turn is.** A locked thread on its own high-resolution timer at normal priority (c) did
  no better than Go, and worse in A and B (99th percentile 69 to 93 ms): it waits in line with every other thread.
- **Priority is the lever.** `TIME_CRITICAL` alone (p) or MMCSS alone (m) brought the 99th percentile to about 1 ms or under in runs
  A to D and G, with single stalls of up to 59 ms. **Both together (d) were the steadiest**: in runs A to D, F and G, median 0.32 to
  0.38 ms, 99th percentile 0.67 to 0.98 ms, worst pulse 1.9 to 11.3 ms, interval jitter 0.23 to 0.43 ms RMS, at 80 to 100% load and the
  same CPU as Go's sleep (10 to 20 million cycles a second). Pass 1, the machine's worst moment, broke it (85 ms), and so did the
  collector (below). A thread under MMCSS alone read 18 from `GetThreadPriority`, which is not one of the levels that
  function documents ([V-web] [GetThreadPriority](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getthreadpriority));
  as an offset from the normal class's 8 it is 26, the top of MMCSS's High range [Inf]. With `TIME_CRITICAL` set after MMCSS (d) it read
  15, so which priority d's thread really ran at is not known; that d was steadier than either alone is measured, why is not.
- **Spinning is a trap.** Waking 1 ms early and spinning on the counter (s, q, e, f, h) took the median to about 1 µs and cost five to
  eight times the CPU (60 to 100 million cycles a second, 0.55 to 0.7 ms of spinning a pulse). With headroom (A) the spinning
  priority strategies were the best of all (h: 99th percentile 0.51 ms, worst 2.7 ms). Under full load (B, D) the spinning thread
  stalled for 2 to 13 seconds, at `TIME_CRITICAL` (q) as under MMCSS (e), and even as found (G, about 90% busy) q and e stalled for
  0.3 to 0.55 s while d's worst was 1.9 ms. With Go's asynchronous preemption off (C), the worst stalls of q
  and e fell from 2.2 and 3.0 s (D) to 0.34 and 0.14 s, but those of f and h did not (0.45 and 0.27 s, against 0.14 and 0.36 s in D). So part of it is Go: the runtime preempts a goroutine that
  holds its processor for 10 ms by suspending its thread from another, ordinary-priority thread [V-code: `retake`, `preemptM`], which a
  saturated machine may not run for a long time [Inf]. Part is Windows: MMCSS drops threads that "have used their quota of CPU resource" to 1 to 7 [V-web],
  and a thread that never blocks competes with the starved threads Windows boosts [Inf]. Either way, **a thread that blocks in the
  kernel between pulses is safe, and one that spins is not.**
- **Power throttling and `timeBeginPeriod` changed nothing measurable on mains** (f against e, h against f, all within the runs' noise).
  They act on battery and on processes Windows ranks as background: Low and Utility QoS change frequency and cores "On battery" only
  ([V-web] Quality of Service, above), and this test ran on mains, from a console whose window decided its QoS [Inf].
- **The wake-up delay is steady, and aiming early removes it.** The prioritised timer woke 0.32 to 0.42 ms after it was due at the
  median in every run. A steady delay is latency, not jitter. Strategy r aims the timer early by the median of the last 32 wake-ups:
  in G its median was 2 µs, its 99th percentile 0.59 ms, its worst 3.9 ms and its interval jitter 0.29 ms RMS, against d's 0.38, 0.72,
  1.9 and 0.23 ms; in F, with sends, 3 µs, 0.83 ms, 12.4 ms and 0.47 ms. It moves the whole distribution onto the grid, but its tail
  and its jitter were a little worse than d's in every run where both ran (worst 3.9 to 20.5 ms against 1.9 to 7.5 ms): a lead that
  moves with each wake-up passes the wake-ups' own jitter on. A slower lead (a longer window, or a step of a few microseconds a pulse)
  should keep d's tail [Inf], and has to be shown to.
- **Go's clock is coarse here.** `time.Now`'s monotonic reading moved in steps of 0.57 to 1.0 ms at the median, with the tick at 1 ms;
  `time.Sleep(time.Millisecond)` took 1.5 to 1.6 ms at the median with headroom and 5.8 to 8.5 ms at full load. The hub's own timing
  test stamps pulses with `time.Now` [V-code: `mydi's tests, clock_timing_test.go`], so the earlier figure of 0.6 ms
  off the grid was read on a clock that moves in steps of about that size.
- **The garbage collector is the Go time thread's real enemy.** With an allocation loop in the same process (run E: 240 to 590
  collections in each strategy's 45 s, 2.1 to 3.6 s of stop-the-world pauses in all), every strategy fell apart, the prioritised ones
  included: d's 99th percentile was 824 ms and its worst 1.1 s, r's 97 ms, e's 84 ms. A locked thread that wakes during a stop-the-world
  pause waits for the world to start again before it runs any Go code [Inf, from the GC guide and the runtime's system-call return
  path]. Run E is extreme, an allocation loop on its own core with the machine at 99%. At a moderate rate (E2: 20 and 100 MB a second,
  12 and 67 collections in 45 s), d held its 99th percentile at 0.91 and 0.95 ms, but its worst pulse grew to 7.5 and 3.6 ms, and
  Go's sleep (a) reached a 99th percentile of 573 ms at 20 MB a second. The longest stop-the-world pause in a strategy's segments was 17
  to 189 ms: on a busy machine the runtime's own ordinary threads, which must stop and start again, wait their turn [Inf]. **A time
  thread that blocks in the kernel survives a moderate collector; it does not survive an allocating process on a saturated machine.**
- **WinMM's call costs about 0.2 ms here, and can hold an ordinary thread for long.** Each `midiOutShortMsg` to the Microsoft GS
  Wavetable Synth took 0.21 to 0.22 ms at the median and 0.36 to 0.41 ms at the 99th percentile from a prioritised thread (worst 2 to
  80 ms); from Go's ordinary goroutine (a) its 99th percentile was 71 ms and its worst 266 ms, and that strategy's pulses fell seconds
  behind (run F). On this machine the call goes through `midisrv` [Inf, from the routing above], so the send's own cost is part of the
  timing, and the send belongs on the prioritised thread. d and r kept their shape with the sends: d's 99th percentile 0.98 ms and worst
  3.9 ms, r's median 3 µs, 99th percentile 0.83 ms and worst 12.4 ms.

## The time thread as built

Steps 1 to 4 of the plan below, and step 5's counters in `status`, are built in the package (`scheduler.go`, `timeline_windows.go`)
and measured on the same laptop with the hub's own timing measure (`TestClockTiming`), each pulse stamped with
`QueryPerformanceCounter` at the port, 120 BPM, 20 s (15 s where marked) [V-exp]:

| Case | Off the grid, median / p99 / worst | Longest gap (ideal 20.83 ms) | Pulses over 5 ms late |
|---|---|---|---|
| Idle, three runs | 0.21 to 0.54 / 0.51 to 0.85 / 0.83 to 0.86 ms | 21.9 to 22.0 ms | None |
| Sixteen goroutines allocating flat out in another process, every processor busy, two runs (one before the thread's path was made allocation-free) | 0.41 to 0.46 / 0.75 to 0.83 / 2.3 to 3.4 ms | 23.5 to 24.7 ms | 0 of 960 |
| Four goroutines in the same process allocating 50 MB/s (13 collections a second) | 0.29 / 0.60 / 11.9 ms | 32.8 ms | 1 of 960 |
| The same at 200 MB/s (45 collections a second) | 0.31 / 0.72 / 7.4 ms | 28.8 ms | 3 of 960 |
| Two, eight and sixteen goroutines in the same process allocating flat out (15 s) | 0.29 to 1.14 / 4.1 to 25.6 / 7.4 to 102 ms | 28.6 to 124 ms | 7 to 15 of 720 |

Each run is 720 to 960 pulses, so its 99th percentile is its 7th to 10th worst pulse and its worst is one pulse: read the figures to the
nearest millisecond. The thread got MMCSS and `TIME_CRITICAL` as an ordinary user's process (`mmcss+time-critical`), and its lead settled at 0.36 ms idle
and 0.85 to 0.9 ms under load [V-exp]. Two findings beyond the spike. First, **the thread's own allocations were most of what the
collector cost it**: before its path was made allocation-free (the counter and the timer's due time kept on the thread's own record,
each call made by its address, the pulse's byte a single slice), and before the measuring port stopped allocating on the thread, two
goroutines allocating flat out in the same process gave gaps of 340 ms and a grid 1.4 s behind; after, the same load gave a worst gap
of 36 ms [V-exp]. An allocation during a collection makes a goroutine do the collector's work, which the thread's priority cannot
shorten [Inf, from the runtime's assist rule]. So the rule the ADR states, that the thread allocates nothing, is the one that matters
most, and `go build -gcflags=-m` is how it is checked. Second, **load from another process no longer reaches the clock at all**, but an
allocation rate far above any program's still does: the rest is the collector's own pauses, which only the process of its own (step 11)
or the system's scheduled send (step 7) take away [V-exp, Inf]. The program's real allocation rate is step 11's first measure, below.

### In the program

The program now reports its own runtime in `status.json` (bytes allocated and collections since it started, its heap), and
the program's MIDI test --soak 60 --soak-port <id>` measures it in the real program: the server side as built, a real client, the clock at 120 BPM
on the laptop's one real output (Microsoft's GS Wavetable synth, note offs only, so silent), 16 note offs a second sent through the client
and a program's link, and 4 notes a second played in. Two runs [V-exp]:

| Window | Allocated | Collections | Heap | Clock pulses over 5 ms late | Longest gap |
|---|---|---|---|---|---|
| Idle, the client running, 10 s | 50 to 121 KB/s | 0 to 6 a minute | 2.1 to 2.2 MB | | |
| The clock alone, 10 s | 71 to 100 KB/s | 6 a minute | 1.9 to 2.1 MB | 0 to 3 of about 400 | 22 ms (second run) |
| The clock with 16 notes a second out and 4 in, 60 s | 388 to 415 KB/s | 27 to 30 a minute | 1.8 to 2.0 MB | 21 to 35 of about 2,900 | 50 and 434 ms |

So **the program allocates about 0.4 MB/s at a musician's busiest**, a hundred to five hundred times below the rates at which the
collector was shown to hold the clock above; the collector is not what holds it in the program [V-exp, Inf]. But the clock in the program,
with notes, is not yet as good as the hub alone: about 1% of its pulses over 5 ms late and, once, a 434 ms gap. The hub alone on the
same real port held under each part of what the program does, 20 s each [V-exp]: the clock alone (p99 0.57 ms, worst 1.7 ms); 16 note offs
a second written beside it from an ordinary goroutine (p99 0.77 ms, worst 4.7 ms, 1 pulse over 5 ms); the machine's ports listed every
2 s, as a program's `Listen` does, though a listing took up to 265 ms (worst 2.3 to 2.9 ms); and notes beside the clock with every
processor busy in another process (worst 3.0 ms, none over 5 ms). So neither the system's MIDI call, nor the port's lock, nor a device
listing, nor a busy machine explains it alone. What remains is something of the running program's own, which a Go execution trace of the
program through a soak would show; failing a fix found there, ADR-0003 part E's hub in a process of its own takes a program's whole
process out of the clock's way [Inf].

### The program traced

`--trace <file>` records a Go execution trace of the program, each pulse more than 5 ms late marked in it, and a throwaway reader
(the `golang.org/x/exp/trace` parser) says for each late pulse what the clock's goroutine did between its due time and its write. In the
first traced soak, ten pulses were over 5 ms late [V-exp]: four were held by the collector's stop-the-world pauses (its "sweep
termination" took 20 to 55 ms on a 4 MB heap, and the thread, ready, could not run Go code until it ended), one by the port's lock, which
a note's writer held while the clock waited, one inside the MIDI send itself, which still allocated through `LazyProc.Call`, and the rest
in the system's timer and calls returning late. Two changes followed: every write of a port is the time thread's (an event for now
too, its caller waiting for its write, so a port's failure still reaches its listener), and the send calls by address. Then the collector
was run a quarter as often (`GOGC=400`, a 7 MB heap in place of 5 MB). Two pairs of traced soaks, one in each order [V-exp]:

| Program | Runs | Pulses over 5 ms late, with notes, of about 2,900 | Longest gap | Collections a minute |
|---|---|---|---|---|
| Before the two changes, untraced (the soaks above) | 2 | 21 and 35 | 50 and 434 ms | 27 to 30 |
| Before the two changes, traced (its heap larger, so fewer collections) | 1 | 9 | 69 ms | 10 |
| Single writer, send by address, default collector | 1 | 7 | 39 ms | 9 |
| The same with `GOGC=400` | 2 | 2 and 1 | 38 and 35 ms | 2 |

How often the collector runs is the lever: at the same rate the two changes moved 9 to 7, and a quarter of the collections moved 7 to 1
or 2 [V-exp]. Each row is one or two runs of a minute on one laptop: the figures are this machine's, read to the nearest pulse.

One more run, the first of a pair, is left out of the table and said here: in it the whole machine stalled, the clock thread's own timer
returning 861 ms late at its high priority and the MIDI send blocking for 793 ms, and the pulses over 5 ms late were 81. What makes a
time-critical thread's wait return so late is the operating system's, not a program's [V-exp, Inf]. The program now runs the collector a
quarter as often while MIDI is on. The pauses that remain are the collector's; the process of its own (ADR-0003 part E) is what takes
them away, and these figures are what it is measured against.

### One slow port, and the runtime under contention

2026-11-15, on the same Windows 11 desktop (16 logical processors) [V-exp, the clock measure `TestClockTiming` built for Windows and run
natively, 20 s at 120 BPM on a stand-in port unless named; load from other processes by 16 spinning processes at normal priority, and in
the process by four goroutines allocating 200 MB/s].

- **One port held every port.** A software port's driver (the GS Wavetable Synth) held writes up to 961 ms after a USB synth was unplugged
  and plugged back, and 10 to 100 ms for about 50 s after, in a process that never opened the synth; on stand-in ports a driver holding
  one port 300 ms silenced another port's clock 301 ms. A hardware port (the synth's own) clocked while a USB stick came and went had
  no write over 20 ms after its first 8 s. ([The record](following-a-midi-clock.md#a-cable-pulled-case-f15).)
- **A writer per port, each write handed over, missed its budget.** Built as ADR-0006 first proposed (every write handed to the port's
  own thread at the time thread's priority): the handoff's median was 40 µs in every run, but its 99th percentile with every core busy
  was 0.68 to 1.51 ms against the 0.5 ms set beforehand, and the pulse's 99th percentile against the grid over 1 ms in several loaded runs.
- **A stall of 330 ms, traced.** One loaded run of that build stalled the time thread 330 ms, and a traced repeat 229 ms. Go's execution
  trace shows the time thread's wait returning during a collection's stop-the-world and the thread left runnable 155 ms after it ended,
  and in another run 73 ms with no collection: a goroutine locked to its OS thread that comes back from a wait without its processor gets
  one through the runtime's other threads, which run at the process's normal priority, and with every core busy in other processes they
  were starved, though the time thread itself was time-critical [V-exp, the trace; Inf: the mechanism, from the runtime's scheduling of
  locked threads]. The design before the writer had the same exposure (stalls of 19 to 54 ms loaded); the writer doubled it.
- **The process above normal.** With the process's class `ABOVE_NORMAL_PRIORITY_CLASS`, the same loaded runs had no stall: worst
  pulses 1.0 to 3.5 ms and longest gaps 22.3 to 25.5 ms in six runs, both designs. The handoff's 99th percentile stayed 0.53 to 0.70 ms.
- **What was taken (ADR-0006):** the process above normal; every port written by the time thread itself until its driver holds a write
  past 20 ms, then moved to a thread of its own. Measured: idle, with the collector loaded and with every core busy, seven runs, the
  pulse's 99th percentile 0.61 to 0.82 ms, the worst 0.7 to 2.3 ms, no pulse over 5 ms late, the longest gap at most 24.0 ms. Into the
  synth's own port, 60 s: 99th percentile 0.92 ms, worst 2.26 ms, the tempo a receiver reads beat by beat from the writes 119.36 to
  120.74 BPM, 99% within 0.35 BPM; the synth's own display, watched, flickered between 119.5 and 120.5 while the clock ran and
  showed 120.0 once it stopped: the receiver reads about a beat, and the clock's sub-millisecond tail (a 99th percentile near 0.9 ms a
  pulse) is half a BPM on it. Not fixed: a pulse that lands on its time, not up to a millisecond after, is what would remove it (the
  thread's own last stretch to its time, which ADR-0003 left out for the risk of a spin, or Windows MIDI Services' scheduled send).

### Landing a pulse on its time, against a synth's display

2026-11-15 [V-exp: the same desktop; the hub's own clock at 120 BPM, 60 s into the synth's own USB port, its tempo display watched by
the owner; and the clock measure on a stand-in port, idle and with every core busy]. A receiver that reads tempo over about a beat turns
each pulse's lateness into tempo: two pulses a beat apart that land 1 ms differently read about 0.25 BPM off at 120. The thread wakes on its
timer and writes on waking, so each pulse lands up to a millisecond after its time. A landing (`mydi.SetLanding`, off by default) wakes the
thread that much earlier and has it wait the last stretch to each write's time on the counter, never longer than the landing and the lead:

| | No landing | Landing 0.3 ms | Landing 0.7 ms |
|---|---|---|---|
| Idle: 99th percentile off the grid; spacing RMS | 0.54 ms; 0.27 ms | 0.73 ms; 0.21 ms | 0.23 ms; 0.10 ms |
| Idle: tempo beat by beat, 99% within | 0.20 BPM | 0.33 BPM | 0.10 BPM |
| Every core busy: 99th percentile; spacing RMS | 0.71 ms; 0.35 ms | 1.56 ms; 0.47 ms | 1.51 ms; 0.49 ms |
| Every core busy: tempo beat by beat, 99% within | 0.30 BPM | 0.58 BPM | 0.52 BPM |
| The synth's display, idle | flickered 119.5 to 120.5 | | within 0.1, one spike of 0.7 |

The display follows the hub's own tail: tighter pulses, a steadier display, so the flicker is neither the display's reading nor USB's
frames. But the landing halves the tail only on an idle machine and doubles it with every core busy [Inf: MMCSS takes a registered
thread's priority down for its reserve to the rest of the system once the thread runs long, and waiting on the counter is running], which
is the spike's own lesson about spinning; so it stays off, and a landing that turns itself on while the thread wakes cleanly and off while
the machine is contended is the next thing to measure. The same run showed a hardware port hold a write too: the synth's own port held one
130 ms, about 23 s in, with nothing plugged or pulled and the machine idle; written by the time thread, the whole clock waited, the port
then moved to a writer of its own (ADR-0006) and its 1,764 writes after went through it with a handoff 99th percentile of 0.21 ms; the
display's one spike was that hold's caught-up pulses.

### Handed ahead

2026-11-15 [V-exp: as above]. After the 130 ms hold, every port was given its writer from the start, and the time thread made to hand
each timed write 2 ms before its time, the writer waiting for it on its own timer (ADR-0007). Seven runs (idle twice, the collector twice,
every core busy three times): a pulse's 99th percentile against its place 0.68 to 0.84 ms (the fallback's 0.61 to 0.82), the worst 0.8 to
4.0 ms, none more than 5 ms late, the longest gap at most 25.6 ms, the tempo read beat by beat within 0.14 to 0.28 BPM 99% of the time;
the writer's own wake against each write's time a median of 0.02 to 0.03 ms and a 99th percentile of 0.55 to 0.90 ms. The handoff's cost
is off the write's path, and a port held 300 ms holds no other from its first hold.

### As a service

2026-11-15 [V-exp: the same desktop; `mydi timing --service` installed as a Windows service under its own virtual account,
run once idle and once with every core busy, 40 s each, on a stand-in port and the synth's own port, then removed]. The service's threads
got MMCSS "Pro Audio" and `TIME_CRITICAL`, its process the above-normal class, and the synth's port opened from session 0. Idle: a
pulse's distance from its grid a median of 0.85 ms, a 99th percentile of 1.26 ms and at most 1.34 ms, the spacing 0.29 ms RMS, the tempo
read beat by beat within 0.24 BPM 99% of the time, no pulse more than 5 ms late. With every core busy: 0.65, 1.00 and 15.1 ms, 0.66 ms RMS,
within 0.36 BPM, two pulses more than 5 ms late, the worst with the synth's driver holding one write 16.6 ms. As a desktop process into
the same port (60 s idle): 0.53, 0.92 and 2.26 ms. The beat-to-beat steadiness is the desktop's; the idle 99th percentile is about
0.3 ms higher in this one run, past ADR-0003's 1 ms. Not covered: the program itself as its service, an hour, the default tick, a loopback.

**Again, longer and counted at the synth (2026-11-17)** [V-exp: `mydi timing --service --seconds 300 --port <synth> --back
<synth>` installed as a service under its own virtual account by an administrator's script and removed after it, 5 min idle, then 5 min
with every core busy at normal priority; the landing on at 1 ms and the lead off, as now by default; the synth's USB set to MIDI alone;
each report with the system's timer tick, read at its start and its end]. Again MMCSS "Pro Audio", `TIME_CRITICAL` and the above-normal
class, granted in session 0, and the synth's port opened for output and input there.

| As a service | Idle, 5 min | Every core busy, 5 min |
|---|---|---|
| Pulses written, back at the synth | 14,400, 14,400 | 14,400, 14,400 |
| Off the grid: median, 99th percentile, worst | 0.12, **0.87**, 7.7 ms | 0.35, 1.11, 110.6 ms (one write held 111 ms) |
| Spacing RMS; tempo beat by beat, 99% within | 0.31 ms; 0.29 BPM | 1.01 ms; 0.26 BPM |
| At the synth, 99th percentile; tempo 99% within | 1.17 ms; 0.24 BPM | 1.32 ms; 0.36 BPM |
| Landed late; stand-downs; resyncs | 79 of 9,792; 22; none | 83 of 8,064; 28; none |
| The tick, start and end | 1 ms, 1 ms | 1 ms, 1 ms |

- **As a service the clock now meets part D's 1 ms idle** (0.87 ms, against 1.26 ms in the first run, before the landing and the
  writers), and misses it by a tenth under full load; every pulse came back. The one hold, 111 ms, was a write Windows held, as on the
  desktop, and was caught up.
- **The writer's own count reads worse here than the measure, and why:** it counted 4,355 and 5,049 pulses more than 1 ms late (30 and
  35%), where the measure's 99th percentile was 0.87 and 1.11 ms. Both stamp a pulse when its write call returns; the writer measures from
  the pulse's due time, the measure from a grid fitted to its first pulse. So the count holds a near-constant delay from a pulse's time to
  its call's return, about a millisecond as a service in session 0 against much less on the desktop (1.24% in the idle hour) [Inf: the
  call through the MIDI service from session 0 takes longer]. A constant delay is latency, which a receiver does not hear as time; the
  spread it does hear is the measure's and the synth's. The count, which `status` shows, overstated lateness for a service by this delay.
  **Fixed (2026-11-18):** each port's writer follows its steady delay from a pulse's time to its call's return as a running median that
  moves at most 50 µs a pulse, reports it (`writeDelayMs`), and the late counts are of lateness past it; a hold still counts, since it
  moves the median by no more than that (`TestASteadyWriteDelayIsLatencyNotLateness`: a steady 2 ms delay followed, the pulses not
  counted late, a 40 ms hold counted past 5 ms). On the desktop, on the system's software synth's port, the delay read 0.27 ms and 113 of
  27,360 pulses counted late (0.41%) [V-exp: the warm-up run above]; as a service, still to measure.
- **The landing stood down 22 times in the idle five minutes, as a service,** where the desktop's idle runs of the night before had
  not. Not the call's delay: the landing measures before the call. As a service 79 of 9,792 landings came late (0.8%), and the
  stand-down's rule (more than 2 late in the last 96) then trips often by chance alone [Inf, from the counts]. But the warm-up run above,
  a desktop process the next night, stood down 38 times in ten minutes (136 of 15,264 late, 0.9%): so the rate follows the machine's
  state that night as much as the session [V-exp]. Whether session 0 lands later needs both in one sitting, landing on and off, as a
  service and as a desktop process; until then ADR-0008's revisit condition (a landing that flaps) is open, not met or cleared.
  **The desktop half, 2026-11-18** [V-exp: `midi timing --seconds 300 --port <the system's software synth> --landing 1`, then `0`, one
  after the other; the tick at 1 ms]: with the landing on, 0.03 ms median, **0.96 ms** at the 99th percentile, 0.48 ms spacing RMS, 31
  stand-downs in five minutes, 4,992 of 14,400 writes landed; with it off, 0.52 ms median (a steady latency), **0.62 ms** at the 99th
  percentile, 0.43 ms RMS. So that night the landing flapped on the desktop too, and each switch moved the pulses by about half a
  millisecond, which widened the spread: the revisit condition of ADR-0008 met on the desktop that night, where earlier nights the
  landing helped (0.31 ms idle). The service half (an administrator's run) is still to run; a decision on the landing
  waits for it, and for more than one night [Inf: the machine's background work decides how often a landing comes late].
- **The tick stayed at 1 ms:** Windows' energy report (`powercfg /energy`, read only, run before the service) named five programs of
  this desktop asking for a 1 ms timer, among them one that is not to be closed. So neither run was at the default tick (15.625 ms);
  a run at the default tick needs the others closed and is still to do [V-exp: the energy report; the reports' `tick`].

### Where it comes back: through a synth's clock send

2026-11-16 [V-exp: the same desktop, the synth over USB, its clock received and its clock send on; `mydi timing --port
<synth> --back <synth>`, the hub's own clock at 120 BPM for 10 minutes, every pulse that came back stamped by the system and counted].
Until now the clock was measured as the hub wrote it; a synth that re-sends the clock it receives (its clock send) sends it back out, so here it is measured as the
device had it, counted pulse by pulse. Step 1's record found the synth sending nothing of its USB input back; with its clock send on it
re-sends each pulse within about half a millisecond, 5.8 ms catch-up bursts included (the burst test below); with it off nothing comes
back, so what was first taken for its MIDI thru is its clock send (2026-11-16). A second device tried first (a drum machine over USB) sent nothing back with its own thru on.

| | 10 minutes at 120 BPM |
|---|---|
| Pulses written, and come back between Start and Stop | 28,800 and 28,800: none lost, none added |
| The tempo of the pulses come back, minute by minute | 119.958 (the first minute: the grid anchored on an echo a pulse early), then 120.000 to 120.001 |
| Write to echo, median, minute by minute | −0.573 to −0.547 ms (a write is stamped once its call returns, after the echo's stamp): flat over the ten minutes, so nothing drifts |
| The tempo over a bar (96 pulses) come back, 99% between | 119.91 and 120.09 (as written: 119.95 and 120.05) |
| Over a beat (24 pulses) | 119.65 and 120.36 (as written: 119.82 and 120.18) |
| Over a sixteenth (6 pulses) | 118.59 and 121.45 (as written: 119.34 and 120.67) |
| The hub's writes | a 99th percentile of 0.59 ms, four pulses in 28,800 more than 5 ms late, the worst 8.4 ms |

So the count and the tempo hold where a device receives them: no pulse lost, and no drift over ten minutes. What a synth's display shows
as a flicker of its last digit (±0.1 BPM, read by the owner on the synth during the run) is the spread over a bar: half of it is already
in the hub's writes, the rest is USB's frames and the system's stamps of the echo, which are millisecond stamps refined by the callback.
A steadier display needs each pulse to land closer to its time: the landing (above), off by default since it cost more under load.

A first 20-second run read a tempo spread of ±58 BPM and writes held up to 147 ms: the synth was sending its own clock while stopped (its
clock send on) and a Stop of its own, two clocks on one wire. Run again with its own clock not running, the writes were as ever (0.82 ms
at the 99th percentile), 960 pulses out and 960 back, the last 0.30 ms from its place.

### The landing, through the synth's clock send

2026-11-16 [V-exp: the same loop, two minutes a run at 120 BPM, the landing set with `midi timing --landing <ms>`, every core busy by 16
spinning processes at normal priority for the loaded runs; the display read by the owner during each run, given here as said]. The
hub's side is the writes as the clock measure stamps them (once the driver's call returns); the device's side is the echo, stamped by
the system to the millisecond, which caps what it can show at about ±0.06 BPM over a bar in every run.

| | At the hub: 99th percentile off the grid | Tempo over a bar, at the hub; at the device | Pulses more than 1 / 2 ms off | Woke too late / landed late | The synth's display (the owner) |
|---|---|---|---|---|---|
| No landing, idle | 0.73 ms | ±0.05; 119.91–120.09 | 8 / 2 | — | ±0.1 |
| 0.5 ms, idle | 0.49 ms | ±0.04; 119.92–120.08 | 15 / 6 | 29 / 32 | ±0.1, not always |
| 1 ms, idle | 0.33 ms | ±0.03; 119.91–120.06 | 11 / 2 | 2 / 10 | flicker a while, then steady, in turns |
| No landing, every core busy | 1.01 ms | ±0.07; 119.94–120.06 | 82 / 5 | — | ±0.1, never steady |
| 1 ms, every core busy | 1.41 ms | ±0.11; 119.87–120.13 | 115 / 27 | 53 / 322 | ±0.5, steady at times |

Every run brought back all 5,760 pulses. It is the landing section's finding again, now at the device and with the display read in
every case: the landing tightens the clock on an idle machine (1 ms better than 0.5 ms, and the only setting the display held still with)
and loosens it with every core busy, past what no landing gives. Under load the writer woke in time and still landed late on 322 pulses
in 5,760: taken off the processor while it waited on the counter, which is MMCSS's reserve for the rest of the system [Inf, as above].
The steady stretches that come and go on an idle machine with 1 ms are not seen in the echo, whose millisecond stamps are too coarse
[Inf: a slow slide between the PC's counter and the USB controller's frames would make them]. So a landing that serves both is one that
lands while the thread lands cleanly and stands down while it is taken off mid-wait, which its counts (woke late, landed late) already
tell, pulse by pulse.

### Standing down under load (ADR-0008)

2026-11-16 [V-exp: the same loop and load, [ADR-0008](../adrs/0008-a-landing-that-stands-down.md) built: the 1 ms landing
on by default, each port's writer standing down while more than 2 of its last 96 landed writes were late; the load switched on and off
every 30 s for three minutes in the last runs; the display read by the owner].

| | At the hub: 99th percentile | Tempo over a bar, at the hub | Landed / stood down | The synth's display (the owner) |
|---|---|---|---|---|
| Idle | 0.31 ms | ±0.03 | 5,760 / never | steady |
| Every core busy | 0.76 ms | ±0.07 | 141 / 4 times | ±0.1 |
| The load switched, stand-downs of 8 bars doubling to 64 | quiet 0.49, 0.46; busy 0.96, 0.81 (one run); quiet 0.69, 1.01, 0.68 (another) | | 1,414 and 1,280 / 6 and 5 times | |
| The load switched, stand-downs of 2 bars doubling to 16 | quiet 0.39, 1.72 (a hold, below), 0.52; busy 0.91, 0.61, 0.77 (one run); quiet 0.27, 0.58, 0.49; busy 1.02, 0.95, 1.19 (another) | | 2,368 and 2,985 / 13 and 8 times | steady idle, ±0.1 busy |

The criteria ADR-0008 set are met: idle as the 1 ms landing (0.33 ms), loaded as no landing (1.01 ms here before, ±0.07 over a bar),
never the fixed landing's 1.41 ms and ±0.5 BPM on the display. The constants first proposed (8 bars, doubling to 64) kept the writer
standing down through a quiet half minute after a loaded one (its quiet stretches no better than no landing); at 2 bars doubling to 16 it
lands again within seconds, and a try that fails costs at most three late writes.

**Open: writes held 30 to 177 ms, and 5 pulses lost once** (first taken for a hold after a load ends; the series below shows it is
not). In five three-minute runs with the load switched (two with the first constants, two with the second,
one control with no landing), two had Windows' write call to the synth hold 35 to 149 ms at a time, 20 to 25 s after a load ended, once
eight times in two seconds; the writer caught up as ADR-0006 part C says, and in that burst **5 pulses of 8,640 written did not come back**,
the only pulses lost in any loop run (the rest: every pulse back, about 76,000 in all). Whether the synth did not receive them or did not
send them back is not known. Both runs with holds had the landing on; the control had none; the F14 run had stalls of the same kind after
its load with no landing [Inf: the holds are in the driver's call, after the landing's wait, so the landing is not their cause, and two
runs in five is not enough to say]. Next: many load-off events, the landing on against off, and which side lost the pulses.

**Not the catch-up: the burst test.** The same night, with no load [V-exp: `mydi loopback --out <synth> --in <synth> --pulses
2400 --hold-every 0 --write-hold-every 48 --write-hold-ms 135 --catch-up-ms 5.8`: the loop's writes held 135 ms every 48 pulses and the
owed ones then sent 5.8 ms apart, as a port's writer catches up]: 2,400 sent and 2,400 back, each paired with its own send, through 49
holds and 439 catch-up pulses. So a catch-up burst passes whole through the system, USB and this synth, and the catch-up rule is not what
lost the 5 pulses. The pairing showed one more thing: the first write after each 135 ms pause took its call about 18 ms, its echo back 9 ms
before the call's middle [Inf from the stamps]; a loop's pulse is paired from when its write call began, never its middle.

**Not the load ending: the load-off series.** Six more three-minute runs the same night, alternating a load killed at each off (16
processes exiting) with one whose processes stay alive, spinning 30 s and sleeping 30 s, and exit only after the run [V-exp: the same
loop, ADR-0008's landing on]:

| Run | Back | Writes held past 30 ms |
|---|---|---|
| Killed, 1 | 8,640 / 8,640 | 2 (136, 40 ms), during a load |
| Alive, 1 | 8,640 / 8,640 | 1 (139 ms), 4 s in, before any load |
| Killed, 2 | 8,640 / 8,640 | none |
| Alive, 2 | 8,644 / 8,640 | 23 (31 to 177 ms), from 31 to 53 s: all while the first load ran |
| Killed, 3 | 8,640 / 8,640 | none |
| Alive, 3 | 8,640 / 8,640 | 1 (129 ms), during a quiet stretch |

So the holds are not set off by a load ending nor by processes exiting: they come with no teardown, during a load, in a quiet stretch and
once before any load, more of them while the machine is loaded. Every pulse came back in every run; the four extra in "alive, 2" came with
its holds of 165 to 177 ms [Inf: the synth's own clock, sent while ours paused longer than it waits; the burst test's 135 ms holds brought
none]. The one run that lost 5 stays the only loss in about 128,000 pulses (most likely the synth's own handling of a long hold: the hour again,
below). One aside, unexplained too: with the loads alive
but asleep, the quiet stretches read 0.72 to 0.76 ms, as with no landing, where the killed loads' quiet stretches read 0.42 to 0.65.

**A known limitation, as it stands (2026-11-17).** Windows' write call to a USB MIDI port now and then holds 30 to 180 ms, once 732 ms in
a quiet minute (the hour again, below): in 6 of the 11 three-minute runs above, from 1 to 23 times a run, more often with the machine loaded, on the synth's port as on the system's own
software synth (the slow-port section); its cause is not identified. The hub keeps it to the port it holds (ADR-0006, ADR-0007), catches
up after it without losing the count (the burst test), and reports it (`status`: the writes held and the longest gap). A receiver still
hears it as a hiccup: one pulse 30 to 180 ms late, then the owed pulses a quarter interval apart. Two checks would split the driver from the
device: the synth's USB set to MIDI alone rather than with its audio (a setting of the synth's, [Inf: whether its audio class shares the
pipe with MIDI]), and a trace that stops just after a hold (`mydi timing --hold-flag <file>` writes the file the moment a
write is held past 100 ms; an administrator's script records Windows Performance Recorder into memory and saves it when the file appears).
The first was done (below: holds rarer and shorter, and under the cliff for an idle and a loaded hour); the trace is not pursued (the
owner, 2026-11-17: no Windows Performance Recorder), so the holds' cause stays unidentified [Inf: the MIDI service's own work].

**A device plugged or unplugged while the clock runs (2026-11-17).** Unplugging a second USB MIDI device during a run held the clock's
writes to the synth 58 times past 30 ms over 33 seconds, a dozen past 100 ms, the longest 215 ms [V-exp: the same loop, the trace that the
hold flag stopped was of this, and was not kept]; every pulse the hub owed went out, and the synth sent 16 of its own meanwhile. This
desktop runs Windows MIDI Services (`midisrv` running, Windows 11 build 26200.9911), which every MIDI program's WinMM calls go through
[V-exp: `sc query midisrv`]. Microsoft names a device removed or added while programs run ("surprise removal") as the stack's big known
issue, and its workaround is the stage's rule: "plug in your MIDI devices before opening Cubase or other MIDI apps, and do not unplug them"
[V-web, [Microsoft's announcement of the new stack](https://forums.steinberg.net/t/news-from-microsoft-new-windows-midi-services-midi-stack-rolling-out-to-windows-11-this-week/1022281)].
The project's tracker has reports of the same family: the service hanging when an open USB device is removed
([#651](https://github.com/microsoft/MIDI/issues/651)), WinMM programs hanging while the service enumerates devices, which the maintainer
put down to one vendor's driver ([#939](https://github.com/microsoft/MIDI/issues/939)), and WinMM output staying broken after a disconnect
during a large send ([#1267](https://github.com/microsoft/MIDI/issues/1267)) [V-web, read 2026-11-17]. So the hub cannot prevent it:
the hold is in the service every program shares, and closing and reopening a port does not avoid a lock the service holds [Inf]. What it
can do: say the rule (the guide, the node's help, a program's own checks), and log a MIDI port that comes or goes, which the program already
notices every few seconds, so that a hold after it explains itself. Whether the random holds come from the same service is open.

**The synth's USB set to MIDI alone (2026-11-17).** The same three-minute runs, the load switched every 30 s, with the synth's USB carrying
MIDI only, not its audio [V-exp: four runs, the setting changed by the owner on the synth]: every pulse back in every run; one run in four
held a write past 30 ms, four times, the longest 120 ms, against 6 runs in 11 with audio and MIDI, 1 to 23 times a run, up to 177 ms (and
732 ms in the hour). And the landing stood down far less, landing 5,304 to 6,720 writes a run against about 1,400 to 2,700 with audio on
[Inf: the synth's audio stream keeps the system busier all the time]. So MIDI alone does not remove the holds but likely makes them rarer
(four runs are few), and it is the better setting where a synth's USB audio is not used with the hub. It gives up the synth's USB
audio: where that is wanted beside the hub's clock, the way left is the synth's DIN MIDI through a USB-MIDI interface, which takes MIDI
off the synth's own USB; extra hardware, so an option for later, not a precondition [Inf, not measured]. The holds left cannot be the
synth's audio: they are the system's, most likely the MIDI service's own work [Inf], which a trace of one left alone would show.

### An hour with the real program, counted at the synth

2026-11-16 to 17 [V-exp: the program's MIDI test --soak 3600 --soak-port <synth>`: core and the hub service in the test's
process, the hub's own binary as its program with ADR-0008 and the Stop's recall, a real client; a program's clock at 120 BPM on the synth's
port, 16 note offs a second sent through a client's listener to it and 4 notes a second played in, for an hour; every core busy for a
minute in every ten; the program counting the clock the synth's clock send brought back on the port it listens to, between Start and Stop].

| | One hour |
|---|---|
| Pulses a program's clock wrote, and the synth brought back | 173,306 and 173,305: one short |
| More than 1 ms and 5 ms late; the longest gap; the grid moved | 2,418 and 71; 139.8 ms (a write held); never |
| The program's heap; allocation; collections | 15.3 MB at the end; 288 KB a second; 1.1 a minute |
| The program's processor and memory, at half time | 10 to 15% of one core (about half of it the landing's wait, [Inf]); 70 MB private working set |
| Notes sent through the client | 48,264, 4 of them failed |

This answers the program-in-process question the first soak left (its 434 ms gap, measured before ADRs 0166 to 0168): an hour of the
program's real work beside the clock, a client's notes through its link, and the clock's longest gap was a write Windows held, as in every
loop run. The one pulse short is the second unexplained loss of the night (with the 5 above), one in 173,306; the program counted totals
only, so where in the hour it went is not known, and the soak now reads a program's counts each minute and says when the difference moves.
The 4 notes that failed are not explained either [Inf: the minutes when 16 processes spun at normal priority, a client's Node process
with them, against the test's 10 s timeout]; the soak now says when each fails. The test's own fault, two checks after the soak failing
because the operator's token was minted once and expired within the hour, is fixed (a token minted at each use).

**The hour again, minute by minute (2026-11-17).** The same soak with a line a minute of a program's counts (`--soak-log`) and the load's
switches logged with their times, so each minute is classed quiet or loaded [V-exp: as above].

| | Quiet minutes (47) | Loaded minutes (11) |
|---|---|---|
| Pulses | 135,395 | 31,762 |
| More than 1 ms late | 1,175: 0.87%, so the 99th percentile is under 1 ms | 1,165: 3.67%, over 1 ms |
| More than 5 ms late | 347, 289 of them in the one minute of a 732 ms hold | 684: 2.15%, so the 99th percentile is over 5 ms; 673 of them in the two loaded minutes with holds, 11 in the other nine |

- **The budget:** met on the quiet minutes, just; **missed** on the loaded ones, and not only part D's 1 ms: their 99th percentile is over
  5 ms, past the 2 ms the report's all-core column allows (an earlier version of this section said it was under 2 ms; it was not). Of the
  684 pulses past 5 ms, 673 came in the two loaded minutes that held writes, 11 in the other nine (about 0.04%): the miss is the known
  limitation's holds, not something new under load.
- **The holds:** the longest, 732 ms, came in a quiet minute; others of 155 and 197 ms in quiet minutes too. Holds are not a load effect
  alone, and they set the hour's worst.
- **Whether the 732 ms hold resynchronised the synth:** ADR-0006 part C puts a clock held past a beat (500 ms here) back in its place,
  with Stop, the next sixteenth's Song Position and Continue, rather than catching up. The run's per-port resync count was not logged and
  its folder is gone; but the synth's clock send brought back one Start and two Stops in the hour, as in the first hour, which had no hold
  that long, where a resync would have added a Stop and a Continue. So most likely it did not resynchronise [Inf: the synth re-sends
  transport, as its echo of Start shows]: the rule looks at how long one write call was held, and a 732 ms gap between pulses can be
  several holds in a row, each under a beat [Inf]. The receiver then had the owed pulses as a catch-up burst, where part C meant a resync.
  The program's status now counts resyncs, and the soak's minute log carries them; and the writer now measures how far behind the clock
  is, from the oldest pulse still waiting, so holds in a row past a beat resync as one long hold does (ADR-0006's action items).
- **The count at the synth moves around the holds:** after a 155 ms hold 5 fewer pulses came back the next minute; after the 732 ms hold
  38 more; after the loaded minutes' holds 25 more, then 5 fewer; 173,362 back for 173,306 written in all. So the synth's clock send is a
  clock of its own, following ours: it fills a gap of more than about 150 ms with its own pulses (the burst test's 135 ms holds brought
  none) and sheds pulses as it takes ours again [Inf, from the minutes' timing]. The count at the device holds only while holds stay
  short, and **the earlier "lost" pulses (the 5, and the 1 of the first hour) are most likely the synth's handling of a long hold, not
  pulses lost on the way**; the hub's own count is not in question, since it writes every pulse of its grid, held or not.
- **The heap is a sawtooth, not a leak:** from about 4.5 MB to about 16 MB and back after each collection, every few minutes all hour
  (the collector set to 400 while MIDI runs), its floor 4.4 to 6.9 MB from the first minute to the last; 1,042 MB allocated, 69
  collections. The first hour's 15.3 MB was the top of a tooth.
- **No note failed** (49,313 sent).

### The lead, A/B through the synth's clock send

2026-11-17 [V-exp: `mydi timing --port <synth> --back <synth> --lead=<true|false>`, ADR-0008's landing on in every run; ten
minutes each idle, then two rounds of three minutes each with every core busy]. ADR-0003 kept the lead (each thread's timer aimed early by
its own estimate of its wake-up delay) only if an A/B on the wire showed it helps; the echo's millisecond stamps cannot show a fraction of
a millisecond, so it is judged at the hub, with every pulse counted at the synth.

| | Idle, lead on | Idle, lead off | Busy, lead on (two rounds) | Busy, lead off (two rounds) |
|---|---|---|---|---|
| Back | 28,800 / 28,800 | 28,800 / 28,800 | 8,640 / 8,640 twice | 8,640 / 8,640 twice |
| 99th percentile off the grid | 0.40 ms | 0.31 ms | 0.76, 0.72 ms | 0.75, 0.96 ms |
| Spacing RMS | 0.154 ms | 0.159 ms | 0.33, 0.35 ms | 0.24, 0.32 ms |
| Tempo beat by beat, 99% within | 0.137 BPM | 0.107 BPM | 0.29, 0.32 BPM | 0.29, 0.38 BPM |
| Landed late; stand-downs | 79; 10 | 23; none | 20, 19; 8, 8 | 35, 20; 13, 21 |

With the landing on, the lead does not help: idle it costs a little (the writer woke 1.36 ms early instead of 1 ms, waited longer on the
counter, landed late more often and stood the landing down ten times on an idle machine), and with every core busy the two are even, one
round each. So it is off by default (`mydi.SetLead`), kept as a switch for the measures on Linux and the Pi, whose waiter has none.

### A held clock resynchronised or caught up, at the synth

2026-11-17 [V-exp: `mydi timing --bpm 120 --seconds 16 --port <synth> --back <synth> --hold-after 480 --hold-ms <ms>`, the
write of pulse 480 (10 s in) held on purpose; `--resync-after-ms`, `--resync-pause-ms` and `--hold-drop` change the writer's rule for the run; the
synth's pattern plays a note on step 1 of each bar, sent to the same port, so each bar's note comes back stamped; two runs of each, which
agreed within 22 ms]. ADR-0006 part C catches up a clock held under a beat and resynchronises one held past it (Stop, the Song Position
of the next sixteenth, Continue on it). A review asked whether the limit should be lower, and whether the synth needs a pause between the
Song Position and the Continue. The notes after the hold, against the grid of the first pulse written (late is +):

| Hold | Caught up (part C under a beat) | Owed pulses dropped (`--hold-drop`) | Resynchronised |
|---|---|---|---|
| 100 ms | **0, 0 ms**, every pulse back | +83 to +87 ms | +125 ms (resync from 50 ms) |
| 120 ms | | | +125 ms (resync from 100 ms) |
| 130 ms | **0, −1 ms**, every pulse back | +103 to +125 ms | +125 ms |
| 160 ms | −41 ms, 2 pulses more back than written | +83 to +104 ms | +208 to +229 ms |
| 200 ms | −63, −84 ms | | +166 ms (resync from 150 ms) |
| 300 ms | −188 ms | | +187 ms (resync from 150 ms) |
| 1,500 ms | about −600 ms (67 more pulses back than written) | | +125 ms; with a pause of 100, 200, 300 ms before the Continue: +249, +354 to +376, +479 to +500 ms |

(The held step's own note comes 80 to 107 ms late in every run, whatever the rule: it was due inside the hold.)

- **The synth ignores Song Position.** Whatever position is sent, it resumes where it stopped: after the 120 ms hold, too short for it to
  run on, it stopped at sixteenth 80, was sent 81 with the Continue on 81's time, and played 80's note then, a sixteenth late in every bar
  after; after 1.5 s it resumed from the same place (about sixteenth 91, where its own clock had carried it) whether the Continue came at
  once or 300 ms later, so each pause made it later by the pause. This was in its pattern mode; its makers' users report its song mode
  does not take the position either. The earlier reading of the 1.5 s runs, that it honoured the Song Position and sat 0.1 to 0.15 s late,
  was wrong. So for this synth a resync can never put it back in place.
- **Under about 150 ms, the catch-up puts it back exactly.** At 100 and 130 ms every bar after the hold landed on the grid (0 ±1 ms),
  every pulse came back, and nothing was left over; dropping or resynchronising left it 83 to 125 ms late. Most of the holds measured (30
  to 180 ms) are in this range, so the rule as built is right for them.
- **Past about 150 ms, every rule leaves it off the grid until its next Start.** About 90 to 100 ms after a pulse it expected, it plays
  its next step on its own, and past about 150 ms its clock send fills the gap with pulses of its own. A catch-up is then counted twice and
  it ends **ahead** by what it filled (−41 ms at 160 ms, −63 to −84 at 200, −188 at 300, about −600 at 1.5 s); a resync leaves it behind by
  about a sixteenth plus its wait (+125 to +229 ms); a drop leaves it behind by its wait (+83 to +104 ms at 160). The offset stays until
  the next Start, and the offsets of several holds likely add up: in the hour above the synth sent back 56 more pulses than were written,
  which, if its sequencer counted them too, is about two beats off the hub's grid by the end [Inf, not measured as a phase]. Holds past
  150 ms come a few times an hour with nothing plugged or pulled (155, 197 and 732 ms in the hour), so this is a stage's problem, not a
  rare one; an earlier version of this section called it rare, which was arguing it away.
- **What would put it back:** the synth's clock send says how many pulses it made itself. Heard against written over the hold, a writer
  could owe only the difference and catch up that much; a receiver that sends no clock back cannot be told apart from one that waited
  [Inf, not built]. Short of that, no rule fits every receiver: one that waits is right after a catch-up and wrong after a drop.
- **Measured next:** an hour with the synth's USB set to MIDI alone, its bar note's phase each minute (below).

### An hour with the synth's USB set to MIDI alone, its bar's phase each minute

2026-11-17, 19:32 to 20:32 [V-exp: `mydi timing --bpm 120 --seconds 3600 --port <synth> --back <synth> --hold-flag <file>`,
the synth's USB carrying MIDI alone, its pattern's note on step 1 of each bar sent back on the same port, the desktop left alone with
nothing plugged or pulled and no load added; each bar's note placed against the hub's grid from its first pulse, minute by minute]. It
asks what the section above left open: whether, with MIDI alone, a hold past about 150 ms still comes, and whether the bar moves in an
hour.

| | This hour, MIDI alone | The hour above, audio and MIDI |
|---|---|---|
| Pulses written, back | 172,800, 172,800 (each minute equal but two, which differ by one across a minute's edge) | 173,306, 173,362 (56 more back) |
| The bar's note against the grid | 0 ms in each of the 60 minutes (−1 to +2 ms; the synth stamps in whole milliseconds) | not measured |
| Writes held | one, 117 ms (minute 9), caught up | 155, 197 and 732 ms in quiet minutes, more in loaded ones |
| Pulses more than 1 ms late, 5 ms late | 2,138 (1.24%), 20 (all in the one hold) | quiet minutes: 0.87%, 347 |
| Resyncs; the landing's stand-downs | none; 16 | |

- **No hold reached the cliff, and the bar did not move.** The one hold, 117 ms, was caught up as part C does and the synth's bar came
  back on the grid; every pulse came back. An hour with MIDI alone is as the two settings' short runs suggested: holds rarer and shorter.
- **So, for now, the answer is the setting, not new code:** with a synth's USB set to MIDI alone, the catch-up as built kept it in place
  for an hour, and owing only the pulses its clock send did not make (the section above) would have changed nothing. It is not built.
- **What this does not show:** one hour on one desktop, idle; the earlier hour's loaded minutes held writes more often, so a loaded hour
  with MIDI alone may still bring a hold past 150 ms [Inf]; and the hub's own timing measure, not the program with a client (the same hub and
  writer). Its 99th percentile was just over 1 ms (1.24% of pulses past it), part D's budget missed by a little on an idle machine, where
  the quiet minutes of the hour above met it (0.87%). **The miss is the first two minutes** [V-exp: each pulse's write against the grid
  of the first, minute by minute, and the lander's rule replayed on it]: their 99th percentile was 1.54 ms above the median and 17 of the
  19 stand-downs fell in them, while minutes 2 to 59 held 0.29 ms above the median, 133 of 167,039 pulses (0.08%) past it by 1 ms, most
  in the one hold. So it is not background work spread over the hour but a start's warm-up. **Which start** decides whether it matters: a process's,
  long before a set, or the clock's own Start, which is every song's first minutes. Measured (2026-11-18) [V-exp: `mydi
  timing --seconds 600 --port <the system's software synth> --restart-at 300 --restart-pause 30`, the clock stopped five minutes in and
  started again thirty seconds later in the same process, each segment against its own grid, minute by minute]: after the process
  started, the first two minutes' 99th percentile 1.44 and 0.95 ms above the median, 36 and 29 pulses past it by 1 ms, then 0.47 to
  0.50 ms; after the clock started again, 0.34 and 0.39 ms, 4 and 5 pulses, and the rest the same as before. So the warm-up is the
  process's start [Inf: its pages and caches warming], not the clock's: harmless for an installed program, which starts long before a set.
  One run, on the system's software synth's port; the budget is still counted over whole runs, those minutes included.
- **Where it must reach the user:** the rules are in the hub's guide, the clock's help (`midi@1.1.3`) and `doctor` on Windows: USB
  set to MIDI only, DIN through an interface where the USB audio is wanted, devices plugged in before and none pulled during.
- **The trade:** MIDI alone gives up the synth's USB audio (the section on the setting above). Where that audio is wanted, the release
  rule per port is back on the table, or the synth's DIN MIDI through an interface of its own.

### The same hour loaded, with the program and a client

2026-11-17, 21:26 to 22:31 [V-exp: the program's MIDI test --soak 3600 --soak-port <synth> --soak-log <file>`, the real
program and a client clocking the synth at 120 BPM and sending 16 note offs a second through the client, the synth's USB set to MIDI alone, its
pattern's note on step 1 of each bar heard by the program and told to the test by the client (`barNodes` of the test); every logical core
busy at normal priority one minute in ten, six times (a load script beside it, its switches logged); nothing plugged or pulled]. The
review's question: does the bar hold with MIDI alone under the load that broke the audio hour?

| | Quiet minutes (47) | Loaded minutes (12) | The audio hour, loaded minutes |
|---|---|---|---|
| Pulses | 135,638 | 34,648 | 31,762 |
| More than 1 ms late | 466: 0.34% | 380: 1.10% | 3.67% |
| More than 5 ms late | 34: 0.03% | 5: 0.01% | 2.15% |

- **Every pulse came back:** 173,306 written, 173,306 back, each minute equal (±1 across a minute's edge); the longest gap 138 ms, no
  resync, the grid never moved; 48,410 notes through the client, none failed; the heap a sawtooth of 4.4 to 16.4 MB. No hold reached the
  cliff, under load too, where the audio hour's loaded minutes held writes past it.
- **The bar did not move, though its measure drifted.** The bar's place, against a bar of 2 s from the first note heard, went from 0 to
  −71 ms over the hour, smoothly: about −1 ms a minute, then −3 to −4 ms a minute in the last eight, never a step (a hold the synth ran
  through would move it 40 ms or more at once, and change the count, which never changed). That is the clock the notes were stamped
  with, not the music: the program stamps an event it hears with the system's wall clock, which Windows' time service slews, while the
  hub's grid runs on the performance counter. Measured just after: the wall clock ran 53 ppm slower than the counter (−3.2 ms in a
  minute), and the time service had last synced at 22:19, inside the hour [V-exp: `performance.now()` against `Date.now()` over 60 s;
  `w32tm /query /status`]. With every pulse back and none filled, the synth's bar cannot move against the hub's pulses [Inf, from the
  counts]. The idle hour's bars were stamped on the hub's own timeline (`midi timing --back`), which is why they showed no drift.
- **So the clock holds for an hour on this desktop under load,** with the synth's USB set to MIDI alone: what the review asked before
  calling it done here. The 1 ms budget: met in the quiet minutes (0.34%), just missed in the loaded ones (1.10%), with no pulse a
  receiver could hear as moved.
- **A limit of the measure:** the soak's bar phase uses the wall clock; a soak that should read a drift of the music itself needs the
  event's time on the hub's timeline, which the webhook's event does not carry (its `at` is wall time, for listeners). Not changed: a
  listener's time is rightly the wall's.

## Review, 2026-11-14

A reader's review of this report, taken on its merits; what it changes is in "The plan after review" below.

- **The wire is what a musician hears, and it is not measured.** Every number here is taken at the program. USB-MIDI's 1 ms frames and the
  interfaces' own quantisation (above) can hide a difference of a few tenths of a millisecond at the program, so the spike's ranking of d
  over p and m, and the case for aiming early, may not survive the DIN socket [Inf]. The loopback rig with a logic analyser and two or
  three real synths following the clock comes before any further tuning, not after it.
- **Aiming early improves the median, which a receiver does not follow.** A receiver follows the spacing of pulses, not their phase, and a
  steady delay is latency, not jitter (this report's own words). In the spike, r's lead worsened the worst pulse to improve the median. The
  hub's lead is slower and capped, and the hub's tails held with it, but it was never measured against the hub without it. It is
  provisional: kept only if an A/B on the wire shows it helps the spacing, or a use case needs phase against another clock.
- **The product's case is untested.** MMCSS's priority depends on "Whether the application is in the foreground" (re-read on the page
  itself on 2026-11-14), which a service in session 0 never is. A run as a service under its virtual account in the throwaway machine
  comes before anything else is built on the priority. So does a quiet machine whose tick stays at 15.625 ms: every measure here ran
  with another program holding the tick at 1 ms, and on a saturated development laptop, which a dedicated machine is not.
- **The samples are small.** A run of 720 to 960 pulses has its 99th percentile in its 7th to 10th worst pulse and its worst in one;
  read every figure here to the nearest millisecond, and the ranking of strategies whose differences are below a run-to-run spread like
  c's (6.8 ms to 93 ms with no change) as unproven. The headline's six runs leave out pass 1, where d reached 85 ms: a live show is where
  such moments happen.
- **The targets are not met today.** They are for an hour; the evidence is runs of 20 to 45 seconds, and tails grow with time. d's longest
  gap in run D (31.9 ms) and the hub's at 50 MB/s in its own process (32.8 ms) are already over the 31 ms target, and the program with notes
  reached 50 and 434 ms. Until hour-long runs on the target machine say otherwise, the targets are goals, not results.
- **Windows MIDI Services is not available to ship on.** Its SDK's releases (the API's newest of 2026-11-01) are previews, and their
  notes say of a "Production / Release app": "These WinRT API binaries are not permitted for this use" [V-web, read on the release notes
  on 2026-11-14, [microsoft/MIDI releases](https://github.com/microsoft/MIDI/releases)]. Step 7 is parked until a stable SDK permits it.
- **The labels were re-checked.** The quotations of MMCSS's page, Ableton's MIDI fact sheet and Windows MIDI Services' pages were found
  word for word in each page's own text on 2026-11-14, not through a summarising fetch. golang/go#67066 is closed as completed on
  2026-10-16 by GitHub's own record, its `NeedsInvestigation` label left on it; its change and its 4 ns to 51 ns are in a comment of
  2026-10-09 [V-web, GitHub's API].
- **The program's allocation rate is measured** (above): about 0.4 MB/s at a busy moment, so the collector is not what holds the clock in the
  program. Something of a program's still does, rarely; its trace, or the process of its own, decides it.

### The plan after review

**Superseded (2026-11-16).** This plan and "The plan, in order" below are as written on 2026-11-14. Both were overtaken: the owner chose
to need no extra hardware (no DIN rig, no logic analyser), the service test ran on this desktop rather than a throwaway machine, and a
synth's clock send became the loop at the device. What was decided and measured since is in [ADR-0003](../adrs/0003-one-time-thread.md)
and [ADR-0005](../adrs/0005-a-followed-clock-that-moves.md) to [ADR-0008](../adrs/0008-a-landing-that-stands-down.md), and
what is left in the backlog; the tables are kept as the record of the plan at the time.

It replaces the order of the plan below, whose rows stay as the description of each step.

| Order | Step | Why first |
|---|---|---|
| 1 | Run the hub as a Windows service under its virtual account in the throwaway machine (plan step 2), and trace the program through a soak to find what holds its clock | The priority is untested where the product runs; a program's own stalls are the one known gap |
| 2 | The DIN rig (step 6): a logic analyser and two or three real synths following the clock, with a hardware master as the floor | It measures what is heard, and how much jitter receivers smooth away, which decides whether the rest matters |
| 3 | Hour-long runs on a quiet mini PC at the default tick, idle and with two clients running, through the rig | The targets are for an hour on the target machine; rewrite them from what these show |
| 4 | Decide in-process or a process of its own (step 11) from 1 and 3 | The program's stalls, not the collector, are now the question |
| 5 | The lead (step 3) kept or cut by an A/B on the wire | It buys a median a receiver does not follow |
| 6 | Linux and the Pi (step 8), after Windows' wire numbers; Windows MIDI Services (step 7) parked until its SDK is stable and permitted in production | |

## Recommendation

The plan first, then the design it builds, what carries to Linux and the Pi, how to measure it on the wire, and the numbers to hold
it to.

### The plan, in order

| Step | Change | Evidence it needs |
|---|---|---|
| 1 | **The time thread.** In the package, one goroutine locked to its thread owns every timed write: the clock and events with `at`, one queue ordered on `QueryPerformanceCounter` ticks, nothing allocated per pulse; the grid rule as today. On Windows it waits on its own high-resolution waitable timer; elsewhere, for now, on Go's timer | The clock's own test (`MYDI_TIMING=1`) rewritten to stamp pulses with `QueryPerformanceCounter`, not `time.Now`, and to report median, 99th percentile, maximum, longest gap and interval RMS, as the spike does; the spike's d row reproduced by the hub on this machine, under the same loads; a test that an event with `at` and a pulse due at the same tick are written in order |
| 2 | **Its priority.** MMCSS "Pro Audio" at critical, then `THREAD_PRIORITY_TIME_CRITICAL`; `TIME_CRITICAL` alone when MMCSS refuses; both logged at start | The same measure with the program run as a Windows service under its virtual account (`tools/spikes/a Windows spike/vm-steps.md`), since MMCSS from session 0 and a service's priority were not tried; the measure on a machine whose timer stays at 15.625 ms (no other program running), where the tick's effect on Go's clock shows |
| 3 | **Aim early, never spin.** The timer aimed early by a slow-moving estimate of its wake-up delay, at most 2 ms (since off by default, the lead's A/B) | The spike's r with a slower lead against d, until its median is near zero and its tail and jitter are no worse than d's (the 32-pulse median of r was not: worst 3.9 to 20.5 ms against 1.9 to 7.5 ms); a test of the lead's arithmetic |
| 4 | **The process asks to be served.** HighQoS and honoured timer requests through `SetProcessInformation`, at a program's start, its own process only | The measure on battery, with and without it, on a laptop; and as a service, where Utility QoS applies |
| 5 | **Stalls counted and shown.** Pulses over 1 and 5 ms late, the longest gap and the grid's moves, in `status` and the program's status page; `doctor` runs a ten-second clock and reports it with the power source, the plan and the timer resolution | Tests of the counters; `doctor` on this machine and on a quiet one |
| 6 | **The loopback rig.** A logic analyser or a second machine on the DIN output of a class-compliant USB interface, an hour per case, as "Measuring end to end" below lists | The table of "The numbers to hold it to" below, filled for WinMM on this machine and on a quiet Windows mini PC |
| 7 | **Windows MIDI Services' scheduled send.** A spike: WinRT activation of `MidiSession` and `MidiEndpointConnection` from Go through `combase.dll` without CGo, then `IMidiEndpointConnectionRaw::SendMidiMessagesRaw` with timestamps 20 to 50 ms ahead; if it works, the time thread hands pulses and timed events to it where the SDK runtime is installed, WinMM elsewhere | The rig's table for the scheduled path against WinMM, at full load; the SDK runtime's licence and how it is installed; whether a service may open a session |
| 8 | **Linux and the Pi.** The same time thread, with `clock_nanosleep` at absolute times and `SCHED_FIFO` (the earlier report's steps 9 and 10) | The spike's table on a Pi 4 and a Pi 5, idle and loaded, then the rig |
| 9 | **A shared timeline for events** (the earlier report's step 11), Link's lesson | A test that two calls 30 ms apart land on the same pulse |
| 10 | **The hub's Go**, once a release carries golang/go#67066's change, re-measured: Go's clock would no longer be the tick, which matters for every other timer in the program, though the time thread no longer needs it | The probe's `time.Now` step on the new Go |
| 11 | **The collector kept away.** The program's allocation rate measured with two clients and their MIDI running, kept low, `GOGC` and `GOMEMLIMIT` set from it; if the clock's worst pulse still follows the collector, and step 7 is not available, the hub moves into a small process of its own (a program's binary in a `midi` role) | The program's own allocation rate (`runtime/metrics`) under a realistic load; the spike's run E2 repeated at that rate; for the separate process, the same table from it, with the program allocating hard beside it |

### What the ADRs and guides should change

| Where | Change |
|---|---|
| Linux's step | Add Windows beside Linux: one time thread with its own high-resolution waitable timer, MMCSS and `TIME_CRITICAL`, `QueryPerformanceCounter` as its clock, no spin, the timer aimed early by its recent lateness, the send on that thread, the process HighQoS and its garbage kept low; Windows MIDI Services' scheduled send as the counterpart of ALSA's sequencer, only after its spike |
| ADR-0002 part B | State the timing target as this report's table: at the program and on the wire, measured, not promised |
| ADR-0002 part E | Note that on Windows 11 with Windows MIDI Services, WinMM is served by `midisrv`, and that only the new SDK schedules |
| The program's development guide | The hub's description: the time thread and its priority once built; the timing measure's columns |

### Open questions

| Question | What would settle it |
|---|---|
| Why a thread spinning at priority 15 or under MMCSS stalls for seconds under full load, beyond Go's preemption | A Windows Performance Recorder trace of run B's q and e (context switches and ready times of the spinning thread) |
| Which priority d's thread runs at (MMCSS's 26 or `TIME_CRITICAL`'s 15), and whether setting `TIME_CRITICAL` before MMCSS rather than after changes it | The same trace's priority field over a run, and the spike with the order reversed |
| Whether MMCSS and `TIME_CRITICAL` work the same for a service in session 0 under a virtual account | Step 2's measure in the throwaway VM |
| How Go's timers and the hub behave on a machine whose tick stays at 15.625 ms | Step 2's measure on a quiet machine |
| Whether Windows MIDI Services' scheduler meets its "low microsecond" claim under load, and its added latency for WinMM | Step 7 with the rig |
| How much jitter hardware receivers tolerate before their tempo moves | No maker publishes it; step 6's rig with two or three real synths following the clock |
| How much the program allocates in real use, so how often the collector would hold the clock | Step 11's measure |
| Whether Link's protocol, as opposed to its GPL code, may be implemented in Go (asked by the earlier report) | Link's licence and a protocol description, if Ableton publishes one |

### The design: one time thread, its own timer, its own clock

What follows is the hub's MIDI clock on Windows, built on the hub of ADR-0002 and the grid rule it already has [Inf, from the findings
above; each step's evidence is in the plan].

1. **One time thread per program.** A goroutine locked to its OS thread (`runtime.LockOSThread`, never unlocked, so the thread dies with
   it) owns every timed write: clock pulses and events with `at`. It keeps one queue of due writes, ordered on one timeline, and
   allocates nothing per pulse. Listeners' calls put events on that queue; nothing else writes a timed byte. The send itself
   (`midiOutShortMsg`, about 0.2 ms here) is made on this thread, never on an ordinary goroutine. Untimed writes (`at` 0, panic, note
   offs on release) may still go straight to the port, behind the same port lock.
2. **Its own clock.** The timeline is `QueryPerformanceCounter`, read directly. Neither `time.Now` nor any Go timer is on the path: in Go
   1.27.1 both run on the interrupt time, which moves once a tick. The clock's grid is kept as today, from its start, in counter ticks.
3. **Its own timer.** The thread waits on its own high-resolution waitable timer (`CreateWaitableTimerExW` with
   `CREATE_WAITABLE_TIMER_HIGH_RESOLUTION`), armed relative to the counter for the next due write, and spends the time between pulses
   blocked in that wait. No `timeBeginPeriod`.
4. **Its own priority.** MMCSS "Pro Audio" with `AvSetMmThreadPriority` critical, and `THREAD_PRIORITY_TIME_CRITICAL` as well; `TIME_CRITICAL`
   alone where MMCSS refuses (`AvSetMmThreadCharacteristicsW` fails, for a missing privilege or a disabled service). No real-time
   priority class.
5. **No spin; aim early instead.** (Since measured: the lead's A/B through the synth, 2026-11-17, found it no help with ADR-0008's
   landing on, so the lead is off by default; the landing wakes a fixed millisecond early and waits the rest on the counter.) The thread aims its timer early by a slow-moving estimate of its wake-up delay, at most 2 ms, which
   takes the steady delay (0.3 to 0.4 ms here) out with no busy loop; slow, because a lead that follows each wake-up passes its jitter on. A spin is not used: it buys only the median, costs several times the CPU, and
   under full load let the thread stall for seconds.
6. **Out of the collector's way.** The time thread allocates nothing, so it never assists the collector; the program keeps its own
   garbage low (no per-message allocation on the MIDI paths, buffers reused) and may raise `GOGC` under a `GOMEMLIMIT` ceiling so that
   collections are rare. That is not enough on its own: a stop-the-world pause holds the time thread whenever a pulse falls inside it,
   and on a busy machine those pauses lasted 17 to 189 ms in run E2, because the runtime's ordinary threads that must stop and start
   wait their turn like any other. Where the tail must be guaranteed, the pulses leave Go's world: Windows MIDI Services' scheduled send
   (item 9), or, failing that, the hub in a small process of its own (a program's binary in a `midi` role, as `launch` is), owning the
   ports and the time thread, fed by the program through a pipe, allocating next to nothing, so that its collector runs only when the
   runtime forces one, once in two minutes without other cause (`forcegcperiod` [V-code: `runtime/proc.go`]), on a process with almost
   nothing to stop [Inf].
7. **The process asks to be served.** At start the program tags itself HighQoS and asks that its timer requests be honoured
   (`SetProcessInformation(ProcessPowerThrottling)`, `EXECUTION_SPEED` and `IGNORE_TIMER_RESOLUTION` in the control mask, a zero state
   mask), since a service is Utility QoS by default and runs on efficient cores and frequencies on battery. Its own process only; the
   power plan is the machine's.
8. **Stalls are counted, not hidden.** The grid rule stays (a short stall caught up, pulses never closer than a quarter interval; past four
   pulses the grid moves on). Each pulse's lateness feeds counters the program reports in `status` and the program's status page: pulses more than 1 ms
   and 5 ms late, the longest gap, the grid's moves. `doctor` runs the same clock for ten seconds against no port and says what it saw,
   with the machine's power source, power plan and timer resolution, so a bad machine is found before a show.
9. **Scheduled sends where Windows has them.** Where Windows MIDI Services' SDK runtime is installed, the time thread hands each pulse and
   timed event to the service with its `QueryPerformanceCounter` timestamp a short lead ahead (20 to 50 ms, a tempo change taking effect
   within the lead), through `IMidiEndpointConnectionRaw::SendMidiMessagesRaw`; the service's scheduler, outside Go, then plays them, and
   a program's own stalls shorter than the lead, the collector's included, vanish. WinMM stays the fallback, and the only path until a
   spike shows WinRT activation from Go without CGo.
10. **Later, a shared timeline.** Events placed on the clock's grid (a beat and a pulse) or on a program's timeline, as the earlier
    report's step 11 and a later step proposes: Link's lesson that peers share time, not ticks.

### What carries to Linux and the Pi

The structure carries whole; the calls change [Inf]:

| Windows | Linux and the Pi |
|---|---|
| One locked thread waiting on its own high-resolution waitable timer | One locked thread in `clock_nanosleep` at an absolute time on `CLOCK_MONOTONIC` (the earlier report's step 10) |
| `QueryPerformanceCounter` as the timeline | `CLOCK_MONOTONIC` read with `clock_gettime` (Go's own monotonic reading is that clock on Linux, per the earlier report, unverified) |
| MMCSS "Pro Audio", or `THREAD_PRIORITY_TIME_CRITICAL` | `SCHED_FIFO` on that one thread, with `SCHED_RESET_ON_FORK`, under the unit's `LimitRTPRIO=` |
| The process tagged HighQoS | The CPU frequency governor is the machine's; `doctor` reports it |
| Windows MIDI Services' scheduled send, where installed | ALSA's sequencer queue, only if the Pi's tail needs it (a later step) |
| No spin; the timer aimed early by its recent lateness | The same rule, measured on the Pi, where a spin would cost a larger share of four cores |

### Measuring end to end, with a loopback cable

The spike times the moment the thread wakes and the call returns. A synth hears the byte on the wire. The rig that measures what it
hears [Inf throughout]:

- **The path.** The program's clock out of a class-compliant USB-MIDI interface, a 5-pin DIN cable, into an independent timer. Best: a
  logic analyser (even a cheap 24 MHz one) on the DIN line, which times each `F8`'s start bit to a few tens of nanoseconds with no
  computer in the way; or a second machine's MIDI input timestamped by Windows MIDI Services (100 ns ticks on its own counter). A loopback
  into the same machine's input is the cheapest, but WinMM's input time is "specified in milliseconds, beginning at zero when the
  midiInStart function was called" ([V-web] [MIM_DATA](https://learn.microsoft.com/en-us/windows/win32/multimedia/mim-data)) and shares
  the machine under test, so it is a sanity check, not the measure.
- **What to compute.** From the pulses' arrival times: each interval's error against 20.833 ms (its RMS, 99th percentile and maximum);
  each pulse's lateness against a straight line fitted through all of them (phase error, free of the interface's fixed latency); gaps
  over 1.5 intervals; drift in parts per million against the reference over an hour.
- **Conditions.** The program idle; with two clients running listeners; with every core loaded; with the garbage collector forced busy; on
  battery for a laptop; through WinMM and, once built, through Windows MIDI Services' scheduled send; at 60, 120 and 240 BPM. Each case
  one hour, since the tails live in rare events.
- **The reference.** The same rig, with a hardware clock (any drum machine or sync box as master) in place of the program, to measure the
  rig's own floor and a real interface's quantisation.

### The numbers to hold it to

Proposed targets, at 120 BPM, for one hour; first measured for an hour on 2026-11-17 (the last two rows), before that only on runs under
a minute (see the review)
[Inf: chosen from Wessel and Wright's 1 ms, USB's 1 ms frames, the DAWs' measured clocks
(Sim'n Tonic's median of 0.99 ms RMS, Live on Windows at 2.1 ms) and what d did in the spike]:

| Where | Interval error RMS | 99th percentile of lateness | Longest gap | Under all-core load |
|---|---|---|---|---|
| At the program (the spike's measure) | under 0.4 ms | under 1 ms | under 1.5 intervals (31 ms) | 99th percentile under 2 ms, no gap over 2 intervals |
| On the wire, through a class-compliant USB interface | under 0.5 ms, half the DAWs' median | under 1.5 ms (phase to the fitted line) | under 1.5 intervals | 99th percentile under 3 ms |
| Drift against the reference | none beyond the machine's crystal (PC crystals are "typically ± 30 to 50 parts per million", [V-web] [Acquiring high-resolution time stamps](https://learn.microsoft.com/en-us/windows/win32/sysinfo/acquiring-high-resolution-time-stamps)), and none from the scheduler at all | | | |
| **Measured, 2026-11-17:** the program with a client for an hour on this desktop, every core busy a minute in ten (the hour again, above) | not measured per pulse | quiet minutes 0.87% past 1 ms (under 1 ms, met); 0.31 ms ten minutes idle, no cell, the lead off (the lead's A/B) | 732 ms, a write Windows held, in a quiet minute: **missed** | loaded minutes 3.67% past 1 ms and 2.15% past 5 ms, so over 5 ms: **missed** (673 of the 684 past 5 ms in the two loaded minutes that held writes); gaps of 155 ms and more: **missed** |
| **Measured, 2026-11-16 and 17:** at a synth, through its clock send | 0.15 to 0.35 ms at the hub, echo stamps too coarse to say | the device side capped by the echo's millisecond stamps | as above | every pulse back while holds stay under about 150 ms; drift: none over ten minutes |

So the first row is met on a quiet machine but for Windows' write holds, which set every longest gap; the hub's own thread is not
where they come from (the clock report's known limitation). A dedicated machine is still untested.

A miss of the first row is a program's to fix; a miss of the second with the first met is the interface's or the driver's, and the
answer is a better interface or Windows MIDI Services' scheduled path [Inf].
