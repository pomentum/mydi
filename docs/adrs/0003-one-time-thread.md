# ADR-0003: One time thread owns every timed write, at a priority the system serves on time

| Part | Decision | Status |
|---|---|---|
| A | One time thread per hub, a goroutine locked to its OS thread, owns every timed write (clock pulses and events with `at`): one queue on one timeline, nothing allocated per pulse; each write handed to its port's writer before its time ([ADR-0007](0007-writes-handed-ahead.md)) | Accepted |
| B | Its clock is the system's own (`QueryPerformanceCounter` on Windows), its wait its own timer (a high-resolution waitable timer on Windows), never Go's timers or `time.Now` on the path | Accepted |
| C | Its priority: MMCSS "Pro Audio" at critical and `THREAD_PRIORITY_TIME_CRITICAL`, or `TIME_CRITICAL` alone where MMCSS refuses; the process asks not to be throttled; no spin; the timer aimed early by an estimate of its own wake-up delay (the lead), off by default, a switch (`SetLead`) | Accepted |
| D | Stalls counted, not hidden: pulses more than 1 and 5 ms late, the longest gap, the grid's moves, the lead (`Hub.Timing`) | Accepted |
| E | Later, each with its evidence: Windows MIDI Services' scheduled send; the same thread on Linux with `clock_nanosleep` and `SCHED_FIFO` | Proposed |

**Status:** A to D Accepted, 2026-11-20; E Proposed.
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** [the Windows report](../reports/midi-clock-on-windows.md): Go's timers against the time thread, MMCSS, the lead's A/B
through a synth, an hour's clock as a service [V-exp]; `clock_timing_test.go` (the measure, `MYDI_TIMING=1`), `scheduler.go`'s counts in
`midi_test.go`.


<!-- carried from the program mydi began in, reworded: the record as it was decided and measured -->
## Context

ADR-0002 made the hub's MIDI clock: 24 pulses a quarter note, its grid kept from its start, a short stall caught up, a long one moving the
grid on. It used Go's timers and `time.Now`. The owner names the clock's latency on Windows as what stands between a sequencer and a
machine that is truly operational, and the research measured why: on a busy desktop Go's sleep and ticker are late by tens of milliseconds
at the tail, and the clock that measured the hub was itself coarse. What held was not a finer timer alone but a thread the system serves
first.

## Decision

### A. One time thread

The hub has one time thread: a goroutine locked to its OS thread for its life. It keeps one queue of due writes (clock pulses and events
with `at`) ordered on one timeline, a pulse's entry reused so a running clock allocates nothing. Every timed byte is decided there and
handed, a little before its time and with that time, to its port's own writer, which makes the driver's call at that time
([ADR-0007](0007-writes-handed-ahead.md)); every write of a port is its writer's, an event for now, a release's
note offs and panic included (panic first, [ADR-0006](0006-a-writer-per-port.md) part C).
The grid rule of ADR-0002 part B stays as it is.

### B. Its own clock and timer

The timeline is the system's monotonic counter read directly: `QueryPerformanceCounter` on Windows; elsewhere Go's monotonic clock until
part E. The thread waits on its own timer, a high-resolution waitable timer on Windows (`CreateWaitableTimerExW` with
`CREATE_WAITABLE_TIMER_HIGH_RESOLUTION`), and an event that wakes it when an earlier write is queued. No `timeBeginPeriod`.

### C. Its priority, and no spin

At its start the thread joins MMCSS's "Pro Audio" task at critical priority and sets `THREAD_PRIORITY_TIME_CRITICAL`; where MMCSS refuses,
`TIME_CRITICAL` alone; what it got is logged. The process opts out of power throttling for its execution speed and its timer requests, its
own process only. The thread never spins: it aims its timer early by an estimate of its own wake-up delay that moves slowly (a sixty-fourth
of each difference), at most 2 ms, and writes when it wakes. **Amended 2026-11-17:** an A/B through a synth's clock send found the lead
no help with [ADR-0008](0008-a-landing-that-stands-down.md)'s landing on (0.31 ms at the 99th percentile idle without it, 0.40
with it; even under load), so it is off by default (`mydi.SetLead`), kept as a switch for Linux and the Pi, whose waiter has none.

### D. Counted

The hub counts pulses, those more than 1 ms and 5 ms late, the longest gap between two, the grid's moves and the lead, and a program's
`Hub.Timing` and `mydi timing` report them.

### E. Later

- **Windows MIDI Services' scheduled send:** where its runtime is installed, pulses handed to it 20 to 50 ms ahead, so a stall of the program,
  a collection included, never reaches the output; a spike first (WinRT from Go without CGo, its licence, a service's session).
- **Linux and the Pi:** the same thread with `clock_nanosleep` at absolute times and `SCHED_FIFO`, measured on a Pi 4 and a Pi 5.
- **The hub in a process of its own,** if the clock's worst pulse still follows a program's collector at its real allocation rate.

## Options considered

| Option | For | Against |
|---|---|---|
| Go's timers, as built | No system calls | Late by tens of ms at the tail on a busy desktop; Go's clock moves a tick at a time [V-exp, V-code] |
| A finer timer at ordinary priority | One call | Measured no better than `time.Sleep` at the tail: the thread waits its turn [V-exp] |
| The time thread at Pro Audio and `TIME_CRITICAL` (chosen) | Under 1 ms at the 99th percentile in eight of the spike's ten runs, at a sleep's CPU (5.57 ms in its first pass, 824 ms with an allocation loop in the process); over 5 ms in the hour's loaded minutes, from Windows' held writes [V-exp] | A collection in the same process can still hold it; a service's session not yet measured (since measured once, a marginal miss) |
| A spin before each pulse | A perfect median | Stalls of seconds under full load, several times the CPU [V-exp] |
| Windows MIDI Services' scheduled send | The system plays on time, whatever the program does | Its SDK in preview, from Go only through WinRT without CGo: part E, after a spike |

## Consequences

- Easier: a sequencer can follow a hub's clock on a busy Windows machine, and the program can say how well it kept time.
- Harder: a thread at high priority must never loop; its code stays small and allocation-free.
- Revisit when: the wire's measure disagrees; Windows MIDI Services' scheduled send works from Go; Go's timers move off the tick.
- Not yet shown (after the report's review of 2026-11-14): the targets of the report hold for an hour on a dedicated machine; they are
  goals until then. As a service in session 0, where MMCSS's priority depends on the foreground, measured once (2026-11-15): the
  priority granted, 1.26 ms at the 99th percentile idle, a marginal miss. Part C's aiming early was
  provisional until an A/B on the wire; the A/B (2026-11-17) found no help, so it is off by default.
  Part E's Windows MIDI Services waits for a stable SDK its releases permit in production.

## Action items

- [x] Parts A to D in `internal/midi`, the timing measure stamped with the system's counter, the spike's strategy d reproduced by the
  hub: idle, a 99th percentile under 0.9 ms off the grid and no gap over 22 ms in 20 s; every processor busy in another process, the
  same; 50 to 200 MB/s allocated in a program's own process, a 99th percentile under 0.75 ms and at most 3 pulses of 960 over 5 ms late
  [V-exp, [the report](../reports/midi-clock-on-windows.md#the-time-thread-as-built)]. The thread's path allocates
  nothing: its own allocations, more than the collector's pauses, were what an allocating program cost it.
- [x] The counters, in `Hub.Timing` (what a program shows of them is its own).
- [x] The program's own allocation rate measured with a client and its MIDI running: about 0.4 MB/s at 16 notes a second out and 4 in,
  27 to 30 collections a minute, a 2 MB heap, far below what stalls the clock [V-exp,
  [the report](../reports/midi-clock-on-windows.md#in-the-program)].
- [x] As a service, 2026-11-15 (on the owner's desktop, installed for the run and removed after it, not a throwaway machine): the clock's
  measure (`mydi timing --service`) as a Windows service under its own virtual account in session 0, 40 s idle and 40 s with
  every core busy, on a stand-in port and a synth's own port. The service got what a desktop process gets: MMCSS "Pro Audio" and
  `TIME_CRITICAL` for its threads, the process above normal (ADR-0006 part F), and the synth's port opened from session 0. A pulse's 99th
  percentile against its grid was 1.26 ms idle (0.92 ms as a desktop process on the same port: a miss of this part's 1 ms, in one run of
  each) and 1.00 ms loaded; the tempo read beat by beat within 0.24 and 0.36 BPM 99% of the time, as on the desktop; the loaded run's
  worst pulse, 15.1 ms, came with the synth's own driver holding one write 16.6 ms [V-exp, [the report](../reports/midi-clock-on-windows.md#as-a-service)].
- [x] As a service again, 2026-11-17, 5 min idle and 5 min loaded, counted at the synth, the landing on and the lead off: 0.87 ms at the
  99th percentile idle (meets part D) and 1.11 ms loaded, every pulse back, one write held 111 ms by Windows; at the tick of 1 ms that this
  desktop's programs hold. The writer's late count read 30% there, from a near-constant delay to a call's return in session 0, not
  spread [V-exp, [the report](../reports/midi-clock-on-windows.md#as-a-service)]. Fixed (2026-11-18): part D's late counts
  are past each port's steady write delay (a running median, `writeDelayMs`), so a constant latency is not late and a hold still is.
- [x] The late first minutes (2026-11-18): the process's start, not the clock's Start; the clock stopped and started again in one
  process ran clean from its second Start [V-exp, [the report](../reports/midi-clock-on-windows.md#an-hour-with-the-synths-usb-set-to-midi-alone-its-bars-phase-each-minute)].
- [x] An hour with the real program and a client (2026-11-17): a program's clock on a synth's port, a listener's notes through the client to it,
  every core busy a minute in ten; 173,306 pulses written, 173,305 back through the synth's clock send, the longest gap 139.8 ms (a write
  Windows held), a program's heap 15.3 MB [V-exp, [the report](../reports/midi-clock-on-windows.md#an-hour-with-the-real-program-counted-at-the-synth)].
- [x] The hour again, minute by minute (2026-11-17): the quiet minutes 0.87% past 1 ms, so part D's 99th percentile met on a quiet
  desktop; the loaded minutes (every core busy a minute in ten) 3.67% past 1 ms and 2.15% past 5 ms, so their 99th percentile is over 5 ms,
  past the 2 ms the report allows under load (first written here as under it, which was wrong), 673 of the 684 in the two loaded
  minutes that held writes; the
  longest gap 732 ms, a write Windows held in a quiet minute, so the 1.5-interval gap target is missed, by Windows' write call and not the
  time thread (the report's known limitation); a program's heap a sawtooth of 4.5 to 16 MB with a flat floor, no leak. This ADR's
  consequence that the targets hold for an hour on a dedicated machine still waits for one: the hour above was the desktop's
  [V-exp, [the report](../reports/midi-clock-on-windows.md#an-hour-with-the-real-program-counted-at-the-synth)]. Again
  with the synth's USB set to MIDI alone (2026-11-17): 0.34% past 1 ms quiet and 1.10% loaded, 0.03% and 0.01% past 5 ms, the longest gap
  138 ms, every pulse back [V-exp, [the report](../reports/midi-clock-on-windows.md#the-same-hour-loaded-with-the-program-and-a-client)].
- [x] The lead's A/B (2026-11-17), through the same synth with the landing on (ADR-0008): no help, a little cost idle (0.31 ms at the 99th
  percentile without it, 0.40 with it), even under load; off by default, kept as a switch for Linux and the Pi
  [V-exp, [the report](../reports/midi-clock-on-windows.md#the-lead-ab-through-the-synths-clock-send)].
- [ ] Still, after review: the hub as a service in the program itself on a throwaway machine (the program's own soak, run as a
  Windows service); `mydi timing --service` measures the hub alone as one; hour-long runs at the default
  tick, idle and with two clients, measured by the hub itself and through a loopback on the machine's own interface (no extra hardware,
  the owner, 2026-11-14; ADR-0004 part G); the lead's A/B is done (above). A logic analyser on the DIN output is
  optional, never a precondition.
- [x] The program's own stalls traced (a Go execution trace of the program, `--trace`): the collector's stop-the-world pauses, the port's lock held by a note's writer,
  the system's timer and MIDI call late, and the clock thread's own allocations in the send. Every write of a port made the time thread's,
  the send allocation-free, and the collector run a quarter as often while MIDI is on, which did most of it: with notes, pulses over 5 ms
  late from 21-35 to 1-2 in about 2,900, the longest gap 35-38 ms, in two runs of a minute on one laptop [V-exp, [the report](../reports/midi-clock-on-windows.md#the-program-traced)].
- [x] Freeze cases (2026-11-15), tests of `tests/midi`: a driver's write that does not return holds the thread and every port's timing
  with it, which the thread cannot prevent; the timing now says so while it lasts (`BlockedMs`, read from any goroutine), and once it
  returns how long the longest write took and how many took more than 20 ms (`LongestWriteMs`, `SlowWrites`), in the program's status
  file (`TestAWriteTheDriverHoldsIsReportedWhileItLasts`); the status file is written every few seconds, so a stall shows there that
  late, not within milliseconds. A port lost under a running clock ends the clock at the failing pulse and closes the port, so the
  thread does not try it pulse after pulse; asked again once the port is back, the clock opens it and starts with Start
  (`TestAClockWhosePortIsLostStopsTryingAndStartsAgainWhenAskedOnceItIsBack`). Not taken: a watchdog lowering the thread's priority, since
  the thread waits on its timer and never spins.
- [x] Answered by [ADR-0006](0006-a-writer-per-port.md) (2026-11-15): one slow port holds every port's timing. Every write is the time thread's, so a driver that blocks a write
  holds the clock and every timed event on every other port too. Seen twice on Windows 11 with the Microsoft GS Wavetable Synth (a
  software port that renders through the audio stack): a write held 83.6 ms in a run, and a USB synth unplugged and plugged back held
  writes to it up to 961 ms, then 200 to 634 ms while the device came back, and writes of 10 to 100 ms for about 50 s, in a process
  that never opened the synth [V-exp, [the record](../reports/following-a-midi-clock.md#a-cable-pulled-case-f15)]. Whether a
  hardware port stalls the same is not measured [Inf: the synth is also a USB audio device, and the software synth's output goes
  through the audio engine it rebuilds]. Two answers to weigh, each measured: a program's checks measuring each port's writes and flagging a slow
  one; or a writer per port, fed by the time thread, so a port that blocks holds only its own (which cannot help that port itself).
- [ ] Part E's process of its own, which takes the remaining stop-the-world pauses away, measured against the in-process numbers above.
- [ ] On a machine left at the default tick (as a Windows service: measured twice, above, both at 1 ms). Every timing report now records
  the tick at its start and end (`tick`, `mydi.SystemTick`); on this desktop five programs ask for 1 ms, one not to be closed among them.
- [ ] Part E, each with its evidence, measured through a loopback on the machine's own interface.
