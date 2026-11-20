# ADR-0006: A writer per port: a driver that holds a write holds only its port

| Part | Decision | Status |
|---|---|---|
| A | The time thread wrote each port until its driver held a write past 20 ms, then the port moved to a writer of its own | Superseded by ADR-0007 |
| B | For a port moved, the time thread kept the queue and handed the bytes at the write's time | Superseded by ADR-0007 |
| C | A port whose driver holds a write holds only itself; when it returns, what waited is released by its kind: a short hold caught up, a long one resynchronised, stale notes dropped, nothing that ends a sound ever dropped; panic goes first | Accepted |
| D | A driver call that never returns: its writer is abandoned, its handle taken for dead, and the port opened afresh with a new writer when it is back (`SetStuckAfter`) | Accepted |
| E | A handoff on every write was to be accepted only under numbers set beforehand; it missed them then | Superseded by ADR-0007 |
| F | The process runs at the above-normal priority class, so the runtime's own threads, which hand a time-critical thread back its processor, are not starved by other programs | Accepted |

**Status:** C, D and F Accepted, 2026-11-20; A, B and E superseded by [ADR-0007](0007-writes-handed-ahead.md).
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** [the Windows report](../reports/midi-clock-on-windows.md): a device plugged or unplugged holding the other ports' writes up
to 215 ms for about 30 s, a driver held on purpose [V-exp]; `ports_hold_test.go`, `devices_test.go`, `write_delay_test.go`.


<!-- carried from the program mydi began in, reworded: the record as it was decided and measured -->
## Context

ADR-0003 gave the hub one time thread at a priority the system serves on time, and made every timed write its own, the driver's call
included: writes from other goroutines had held a port's lock while the clock waited, and a handoff between threads is a wake-up the
system may serve late. That holds while every driver returns at once. When one does not, the thread waits in it, and every port's clock
and every timed event wait with it: the thread cannot preempt a call into a driver, only report it (ADR-0003's `BlockedMs`). The record
shows the system does hold writes, for close to a minute after a USB MIDI device comes and goes, and once now and then besides. On a
stage that is every device's clock stopping because one was unplugged. A writer per port moves the wait to the port that waits, and then
what that port does when it returns must be decided: a backlog written back to back is a burst of clock (a tempo spike, which ADR-0004
forbids) and of notes played late (wrong notes).

## Decision

### A. A writer per port, once it holds

The time thread writes each port itself, as ADR-0003 has it, while the port's driver returns at once: a healthy port pays nothing. A
write that holds past a slow write (20 ms) moves the port to its writer for the rest of the port's life (until it is closed and opened
again): the hold that moves it still holds every port, once; the holds after it hold only that port, and the record shows they come in
numbers (after a device comes and goes, 50 s of them). The writer, once started: a goroutine locked to an OS thread of its own for the port's life, at the time
thread's priority (MMCSS "Pro Audio" and `TIME_CRITICAL`, ADR-0003 part C), with its own wake event (the time thread's waiter). It makes
every call into that port's driver, timed or not. It allocates nothing per write: what it is handed waits in a ring made with it. A
writer whose driver spins inside a call rather than blocking is not caught: ADR-0003 took no watchdog, and with several ports there are
several threads at a high priority that could burn a core so.

### B. The time thread decides, the writer writes

The time thread keeps everything it keeps today (the queue, the timeline, the grids, the follower's and the re-maker's decisions) and at a
write's time hands its bytes to the port's writer and wakes it; it calls no driver. What it needs back comes back from the writer: when
each write was made (the echo test and the reports read it, ADR-0004 and ADR-0005), and whether it failed (a port that fails is closed
and a clock on it stopped, as today).

### C. A held port, and what it releases

A port's bytes keep their order, with one exception: **panic goes first** (all notes off on the port's channels), taking any note-on
still waiting out of the ring. When a held driver returns, what waited is released by its kind and how long it waited:

| Held for | Clock pulses | Note-ons | Note-offs, controllers, program changes, other messages |
|---|---|---|---|
| Up to a beat | caught up no closer than a quarter interval (ADR-0004 part D), the count kept | played, unless held past 50 ms: dropped and counted | always written, in order |
| Past a beat, a clock with a count (a followed clock, or the hub's own while its transport runs) | the backlog not replayed: **resynchronised** with Stop, the Song Position of the next sixteenth the count reaches, and Continue on it, so a follower that honours Song Position takes its place again (one synth measured does not, 2026-11-17: the action items) | dropped and counted | always written (no note left sounding); a controller's values collapsed to its latest |
| Past a beat, the hub's own clock with no transport | moved on to the grid, as ADR-0002 part B moves it | dropped and counted | as above |

Each port's longest write, its writes over 20 ms, how long it is held now, and what its returns dropped, caught up and resynchronised are
reported per port (ADR-0003 part D), so a program's status can name the port. The ring holds about 20 s of a clock at 300 BPM with
events; a port held past it is treated as stuck (part D).

### D. A driver call that never returns

A writer still inside a driver call when its port is past the ring, or past a limit of its own, is abandoned: its thread is left where
it is (a thread leaked, accepted), the port's handle taken for dead and never closed from another thread (a close during a call that has
not returned may wait forever or fail), and the port counted as gone. When the port is back (`Revive`, ADR-0005), it is opened afresh
with a new writer, its notes released as for any port back (ADR-0002 part C). Tested with a stand-in whose write never returns. An abandoned writer whose call returns at last writes nothing more: it is marked
abandoned before the port is opened again, and the port's new writer is another (`abandoned`, checked when the call returns).

### E. The price, measured

A handoff is a wake-up of another thread, which ADR-0003 avoided, and a wake-up is cheap on average and dear at the tail. A writer for
every port from its first write (this ADR as first proposed) was to be accepted only if, on Windows, measured on stand-in ports and on
the machine's own:

| Case | A pulse against its grid, 99th percentile | The handoff (handed to written), median | The handoff, 99th percentile |
|---|---|---|---|
| Idle | under 1 ms (ADR-0003's) | under 0.1 ms | under 0.5 ms |
| Every core busy at normal priority | under 1 ms | under 0.1 ms | under 0.5 ms |
| The collector at ADR-0003's load | under 1 ms | under 0.1 ms | under 1 ms |

It missed: the handoff's median was 40 µs in every run, but its 99th percentile with every core busy 0.57 to 1.51 ms at the process's
normal class (and the pulse's over 1 ms in several loaded runs, and a stall of 330 ms), and still 0.53 to 0.70 ms with part F, measured
with part F in three loaded runs (the pulse's 0.68 to 1.02 ms). So part A is the fallback set with these numbers: written by
the time thread until a port holds. Measured with part F, seven runs idle, with the collector loaded and with every core busy: the
pulse's 99th percentile 0.61 to 0.82 ms, the worst 0.7 to 2.3 ms, no pulse more than 5 ms late, the longest gap at most 24.0 ms; into a
hardware port for 60 s, 0.92 ms, 2.26 ms and 23.1 ms.

### F. The process above normal

The process sets its priority class to above normal when the time thread starts (beside ADR-0003 part C's request not to be
throttled), and the timing says so (`processClass`). A goroutine locked to its OS thread that comes back from a wait without its
processor gets one through the runtime's other threads; at the normal class, with every core busy in other programs, those were starved
and the time-critical thread waited, 155 ms after a collection's stop-the-world had ended and 73 ms with no collection, in Go's execution
trace. Above normal, six loaded runs had no stall (worst pulses 1.0 to 3.5 ms). It is this process's own setting, and nothing in it
spins.

## Options considered

| Option | For | Against |
|---|---|---|
| One thread writes every port (ADR-0003 part A, today) | No handoff: a write is made the moment it is due | A driver that holds one write holds every port's clock (301 ms for 300 ms on stand-ins; up to 961 ms on the system's own stall) |
| A writer per port at the time thread's priority from its first write (as first proposed) | A held port holds only itself; the time thread never enters a driver | A wake-up per write, which missed part E's tail |
| A writer per port at normal priority (plain goroutines) | Simple | The system and Go's scheduler serve it late under load, which ADR-0003 measured [V-exp, the spike] |
| **The time thread writes, and moves a port to a writer once it has held a write (chosen)** | No handoff while every driver is quick; a held port isolated after its first hold | The first hold still holds every port, once; two paths to keep right |
| A check measures each port's writes and flags a slow one, nothing else | No change to the clock | Names the problem, does not stop it holding every port |
| WinMM's own scheduled output (`midiStreamOpen`, `midiStreamOut`) | The system plays timed events itself | Its timing is the system's millisecond tick, and how it behaves through Windows 11's MIDI service is unknown [Inf]; the driver call that holds is still made somewhere |
| Windows MIDI Services' scheduled send (ADR-0003 part E) | Bytes handed ahead, the service writes them | Not installed by default; a spike first |

## Consequences

- Easier: a stage keeps its other clocks when one device is unplugged or a driver stalls; `Hub.Timing` names the port that holds; a held
  port comes back with no burst and no wrong note, in its place for a receiver that waits or honours Song Position, and after a hold under
  about 150 ms for one that runs on its own clock; past that, such a receiver is left off the grid (the action items, 2026-11-17).
- Harder: a thread per port at a high priority, a ring per port, the release rules, and the write's time and result coming back from
  another thread; the clock pays a wake-up per write, which part E bounds.
- Linux and the Pi: ALSA's raw MIDI can be opened non-blocking and its sequencer schedules output itself, so the Linux writer's shape
  waits on its own measurement (ADR-0003 part E); the Windows shape is not assumed to carry over.
- Revisit when: a first hold that holds every port matters on a stage (then every port its own writer, at part E's cost); or Windows
  MIDI Services' scheduled send takes the writes off the program altogether.
- Not measured: part F on a machine shared with a DAW (its audio threads under MMCSS sit above the class, which makes it likely fine
  [Inf]), and parts A to F as a service, which ADR-0003's service test has since measured (the class and the threads' priority granted in
session 0). A resynchronised port's followers take their place only if they honour
  Song Position; many synths resume where they stopped, which a program's help should say, and
  each resync is counted. A receiver that runs on its own clock in a gap is left off the grid by a hold past about 150 ms whatever part C
  does (the action items).
- Not changed by it: the clock's own sub-millisecond tail, which a receiver that reads tempo over about a beat shows as half a BPM (a
  synth's display flickered between 119.5 and 120.5 at 120): a pulse landing on its time is ADR-0003's question.

## Action items

- [x] Build parts A to D behind `TestAPortWhoseDriverHoldsAWriteHoldsNoOtherPort` (b's longest gap a third of a's 300 ms hold at most, 22.8 ms seen),
  a stand-in whose write never returns, a held port's return for each row of part C (a burst never closer than a quarter interval, the
  Song Position resync, notes dropped and note-offs kept), panic first, and the suite as it is.
- [x] Part E on Windows (2026-11-15: missed; the fallback and part F measured): the clock timing measure (`MYDI_TIMING=1`, `TestClockTiming`) before and after, idle, with every core
  busy and with the collector loaded, the handoff's median and 99th percentile; then on the synth, a follow and the hub's own clock.
- [x] Not needed once every port has its writer from the start (ADR-0007): the first hold protected: every port moved to its writer when a device arrives or leaves (the system's notice of a device
  change), since the record shows the largest holds come at that moment (171, 212, 961 ms at a pull), and moved back once quiet.
- [x] Not needed (ADR-0007): a moved port moved back to the time thread after about 60 s with no slow write, so it pays the handoff's tail only while it holds.
- [x] A hardware port while a USB MIDI device comes and goes (2026-11-17): a second device unplugged held the synth's writes up to 215 ms
  for about 30 s, Windows MIDI Services' known "surprise removal" issue; the stage's rule is to plug in before and unplug none during
  (the clock report's known limitation).
- [x] Part C's resync past a beat looked at how long one write call was held; a gap between pulses made of several holds in a row, each
  under a beat, escapes it, and its owed pulses go out as a catch-up burst. A 732 ms gap in an hour's run most likely did so (the synth
  heard no extra Stop or Continue; the clock report's second hour) [Inf]. Fixed (the owner, 2026-11-17): after any slow write the writer
  measures how far behind the clock is, from the oldest pulse still waiting, and resyncs past a beat whether one hold or several put it
  there; a short hold is still caught up (`TestHoldsInARowPastABeatAreResynchronisedToo`: five holds of 150 ms at 240 BPM, no resync
  before, a resync after).
- [x] Part C's limit and its resync's shape against a synth (2026-11-17, the clock report's "A held clock resynchronised or caught up"):
  the synth ignores Song Position and runs on its own clock about 100 to 150 ms into a gap. Under about 150 ms the catch-up puts it back
  exactly (100 and 130 ms holds: 0 ±1 ms); past it every rule leaves it off the grid until its next Start (catch-up ahead by what it
  filled, −41 ms at 160 to about −600 at 1.5 s; resync +125 to +229 ms; the owed pulses dropped +83 to +104 ms). A limit of 150 ms or a
  pause before the Continue helped nothing. The switches stay for the next device (`mydi.SetResync`, `mydi.SetHoldDrop`, `midi timing
  --resync-after-ms --resync-pause-ms --hold-drop`, with tests).
- [x] A receiver left off the grid by a hold past about 150 ms (above): an hour with the synth's USB set to MIDI alone, its bar's phase
  logged each minute (2026-11-17): one hold in the hour, 117 ms, caught up; the bar on the grid (0, −1 to +2 ms) in all 60 minutes and
  every pulse back (172,800). So the setting answers it on an idle desktop, and no release rule per port is built; a loaded hour with
  the program and a client is next (the clock report's "An hour with the
  synth's USB set to MIDI alone").
- [x] The same hour loaded (2026-11-17), the program and a client, every core busy a minute in ten: every pulse back (173,306), the longest gap
  138 ms, no resync, the bar never stepping (its measured place drifted only with the wall clock it was stamped with; the clock report's
  "The same hour loaded").
- [ ] Revisit when a hold past about 150 ms reaches such a receiver with MIDI alone (a busier machine, another desktop), or its USB audio is
  wanted beside the clock: then the release rule per port, for instance owing only the pulses its clock send did not make itself
  (`--hold-drop` is already a switch of the measure), in a new ADR.
- [ ] Linux and the Pi: measured first (non-blocking raw MIDI, the sequencer's queue), then a writer's shape for them.
