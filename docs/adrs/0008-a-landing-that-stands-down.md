# ADR-0008: A landing that stands down under load

| Part | Decision | Status |
|---|---|---|
| A | Each port's writer lands its timed writes on their time ([ADR-0007](0007-writes-handed-ahead.md) part C's landing), 1 ms, on by default (`DefaultLanding`, `SetLanding`) | Accepted |
| B | A writer that lands late, or wakes too late to land, on more than 2 of its last 96 writes stands down: it writes on waking, as with no landing, for 2 bars, then tries again; each stand-down that follows another at once lasts twice as long, up to 16 bars (`DefaultLander`) | Accepted |
| C | What each writer did is counted: its writes landed, landed late and woken late, its stand-downs and the time it stood down, in the hub's timing | Accepted |

**Status:** Accepted, 2026-11-20.
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** [the Windows report](../reports/midi-clock-on-windows.md): the landing's A/B, a loaded machine where landing made pulses
later [V-exp]; `lander_test.go`, `write_delay_test.go`.

## Context
Waiting the last millisecond to a write on the counter (a landing) puts pulses on their time on a quiet machine; on a loaded one the
writer is taken off mid-wait and the landing makes the pulse later than writing on waking would.

## Decision
As the table says: the `Lander` watches each writer's last 96 landed writes and stands it down while it lands late, backing off.

## Consequences
- Easier: the best of both: on time when the machine allows, never later than without a landing.
- Harder: a writer's behaviour changes with load, so its counts must be read with the stand-downs.
