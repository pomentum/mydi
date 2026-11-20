# ADR-0007: Writes handed ahead to each port's writer

| Part | Decision | Status |
|---|---|---|
| A | Every output port has its writer from the moment it is opened, so every hold of a driver, provoked or not, holds only its port | Accepted |
| B | The time thread hands each timed write (a clock's pulse, a scheduled event, a following's queued write) about 2 ms before its time, with that time; the writer waits for it on its own timer, so the handoff is never on a write's path | Accepted |
| C | The writer counts the clock's pulses against their place ([ADR-0003](0003-one-time-thread.md) part D's counts), and a landing, when set, is the writer's | Accepted |

**Status:** Accepted, 2026-11-20. Supersedes [ADR-0006](0006-a-writer-per-port.md) parts A, B and E.
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** [the Windows report](../reports/midi-clock-on-windows.md): the handoff's histogram, a clock through a synth for an hour
with writes handed ahead [V-exp]; `write_delay_test.go`, `ports_hold_test.go`, `lander_test.go`.


<!-- carried from the program mydi began in, reworded: the record as it was decided and measured -->
## Context

ADR-0006 gave a port a writer of its own so that one driver that holds a write holds only its port. Handing each write at its time cost
the clock's tail a wake-up of a second thread, which missed its budget, and the fallback set with that budget was taken: the time thread
writes each port itself until the port's driver holds a write, then the port moves to its writer. A review named the price, and a run
then paid it: a hardware port held a write 130 ms unprovoked, and as that port's first hold it held every clock. The holds a stage cares
most about are the first ones (171, 212 and 961 ms as a device was pulled), and a fallback that moves a port only after one leaves every
first hold unguarded. What cost the first design its tail was handing a write at the moment it was due, so the second thread's wake-up
fell on the write's path; handed early, it does not.

## Decision

### A. Every port its writer

When the hub opens an output, it starts the port's writer (ADR-0006 part A: a goroutine locked to an OS thread of its own at the time
thread's priority, its own timer and wake event, a fixed ring). Every call into the port's driver is the writer's, a release's note offs and
panic included (panic first, ADR-0006 part C).

### B. Handed ahead

The time thread keeps its queue, its timeline, the clocks' grids and the follower's decisions. A timed write (a clock's pulse, a scheduled
event, a following's queued write) is handed to its writer `handAhead`, 2 ms, before its time, with that time; the writer waits for it on
its own timer, waking ADR-0008's landing early (a millisecond) and waiting the rest on the counter, and writes it then. (First written
here as aimed early by its own lead as the time thread's; the lead of ADR-0003 part C is off by default since its A/B, 2026-11-17.) A following's own look at
what is due runs at its time, since it decides from what has come by then, and hands what it finds due at once. **So a followed clock's
pulses are handed at their time, not ahead:** the line's pulses are found due by that look, and the handoff, with its tail, is on their
path, as ADR-0006's first design had it for every write; only a following's writes queued for later are handed ahead. Its numbers under
this ADR are not measured yet (the results below are the hub's own clock). A steady line's pulses are predicted and could be handed
ahead; doing so commits a pulse 2 ms before the follower knows whether the source has moved, which ADR-0005's wait for a pulse that came
late relies on, so it waits on that measure.

A write for at once (an event without `at`, Start, Stop, a release) is written as soon as the writer reaches it, after what was handed
before it, which is at most 2 ms ahead. **What is handed is delivered:** a write handed ahead is not recalled, so a Stop asked of the
hub's own clock can follow one more pulse whose time was within the 2 ms (a receiver counts one pulse more than the listener meant, a
24th of a beat); a followed clock's count (ADR-0004 part D) is not changed by it, since its pulses are handed at their time.
**Amended 2026-11-16:** a Stop of the hub's own clock now recalls the pulses still waiting in its port's ring, and one being landed,
so no pulse follows a Stop asked for (the action items); what is handed is delivered for every other write.

### C. Counted where it is written

The writer counts the hub's clock pulses against their place: how many, more than 1 and 5 ms late, the longest gap, a late one marked in a
trace (ADR-0003 part D), which the time thread counted when it wrote them. A landing (`SetLanding`) is the writer's last stretch to
each write's time. **Amended 2026-11-17:** off when written here, the landing is on by default at 1 ms since
[ADR-0008](0008-a-landing-that-stands-down.md), standing down while it lands late.

## Results

Seven runs of each, 20 s at 120 BPM: idle twice, with the collector at 200 MB/s twice, with every core busy three times; the process above
normal (ADR-0006 part F).

| | ADR-0006's fallback | Handed ahead |
|---|---|---|
| A pulse against its place, 99th percentile | 0.61 to 0.82 ms | 0.68 to 0.84 ms |
| The worst pulse | 0.7 to 2.3 ms | 0.8 to 4.0 ms |
| Pulses more than 5 ms late | none | none |
| The longest gap | at most 24.0 ms | at most 25.6 ms |
| The tempo read beat by beat, 99% within | 0.17 to 0.29 BPM | 0.14 to 0.28 BPM |
| A port held 300 ms (stand-in ports) | holds the others only on its first hold | holds only itself, always |

ADR-0003's pulse budget, a 99th percentile under 1 ms, is met in every run. ADR-0006 part E's handoff budget no longer measures anything on
the write's path: what that figure now reads (a write's start against its time) is the writer's own wake, a median of 0.02 to 0.03 ms and
a 99th percentile of 0.55 to 0.90 ms, as the time thread's own was.

## Options considered

| Option | For | Against |
|---|---|---|
| ADR-0006's fallback: the time thread writes until a port holds | No second thread on a healthy port | A port's first hold, which may come unprovoked, holds every port |
| Every port its writer, each write handed at its time (ADR-0006 as first proposed) | Every hold isolated | A second thread's wake-up on every write's path: its tail missed the budget |
| The stuck thread becomes the port's writer; a fresh time thread takes over | No second thread until a hold; isolated after 5 to 10 ms | Every write path restructured so a driver is called outside the port's lock first |
| **Every port its writer, each timed write handed ahead (chosen)** | Every hold isolated; the handoff off the write's path; precision as before (own clock; following not yet measured) | A thread per port at a high priority; a write for at once may wait up to 2 ms behind a pulse handed ahead |

## Consequences

- Easier: no first hold holds the other ports; the writer is where a landing on time would live.
- Harder: a thread per open port at the time thread's priority; two waits, the time thread's to hand and the writer's to write.
- Revisit when: the time thread's own wake-up is later than `handAhead` often enough to show in the pulses (then hand earlier); or a stage
  needs an event for at once ahead of pulses already handed.

## Action items

- [x] Built and measured as above; the suite's holds, release rules and stuck driver pass with every port its writer from the start.
- [x] ADR-0006's action items for a device-change trigger and for moving a port back after a quiet spell: not needed, since every port
  is isolated from its first write.
- [x] As a service (ADR-0003's service test, 2026-11-15, with this ADR's build): every port its writer and the process above normal, in
  session 0 under a virtual account, as measured there (one run each; 1.26 ms at the 99th percentile idle, a marginal miss).
- [x] A followed clock under this ADR (2026-11-16, [F14 again](../reports/following-a-midi-clock.md#f14-again-under-adr-0007-2026-11-16)):
  the synth as master, idle and with every core busy; phase with every core busy a median of 1.43 ms and a 99th percentile of 2.93 (2.59
  before ADR-0006), 9 pulses in 2,304 past a quarter interval, none past half. The run found a follower fault, not this ADR's: a delivery
  the system held, then let go in a burst, counted as pulses missing (fixed there).
- [x] Whether a steady line's pulses are handed ahead: not taken (2026-11-16). A third of a millisecond at the 99th percentile is not
  worth committing a pulse before the follower knows whether the source has moved, the class of fault the follower took two days to
  remove (ADR-0005); a followed clock's pulses stay handed at their time.
- [x] A Stop of the hub's own clock recalls the pulses still waiting in its port's ring, as panic takes note-ons out, so no pulse follows a
  Stop asked for (2026-11-16): taken out of the ring, and one already taken to be landed dropped when a recall came during its wait
  (`recallTicks`, the writer's `recalls`); tested by stopping 20 times 1 ms before a pulse is due, which wrote 2 to 4 pulses after the
  Stop with the ring recalled alone and none once a pulse being landed was dropped too (`TestNoPulseIsWrittenAfterTheClocksStopIsAskedFor`). So Part B's "what is handed is
  delivered" no longer holds for the hub's own clock at a Stop.
