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

## Context
Moving a port to its writer only after its driver had held once left the first hold to hit every port, and handing each write at its
time put the handoff's own latency on the write's path.

## Decision
As the table says. The time thread decides what is written when; each writer waits for the time it was given on its own
high-resolution timer and makes the driver call. `Hub.Timing` reports the handoffs and each writer's counts.

## Consequences
- Easier: no port shares another's hold; the handoff is never late for a write.
- Harder: a write is committed about 2 ms ahead, so a release cancels what was handed but not yet written through the writer.
