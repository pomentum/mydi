# ADR-0005: A followed clock that moves

| Part | Decision | Status |
|---|---|---|
| A | A followed clock is re-made on the line while the source is steady and sent on from the source's own pulses while it moves (a jump, a ramp, a slow drift), never ahead of it; the first pulse after Start is the source's own, and the line moves to it | Accepted |
| B | The echo test is a loop guard, off unless the follow says the hub's own clock can come back into the port (`FollowOptions.LoopGuard`) | Accepted |
| C | Two criteria for each pulse sent against the source's of its place: within half an interval always (identity), within a quarter (quality); and a measured column | Accepted |

**Status:** Accepted, 2026-11-20. Amends [ADR-0004](0004-following-a-devices-clock.md) parts A, C, D and G.
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** [following a MIDI clock](../reports/following-a-midi-clock.md) (a source that ramps, jumps and drifts; a drum machine's
recorded Starts) [V-exp]; `ramp_test.go`, `restart_test.go`, `remake_test.go`, `echo_loop_test.go`.

## Context
A line fitted through the source's recent pulses predicts well while the tempo holds; while it changes, a prediction runs ahead of or
behind the source, and a pulse sent ahead of the source's own is one a receiver counts early. The echo test, run always, took a steady
master that happened to sit near the hub's own pulses for an echo.

## Decision
As the table says. While the follower says the source moves (`Follower.Moving`, `Uneven`), each pulse is sent on when the source's own
comes, never predicted. The echo test runs only where a program says its own clock can come back (a synth's soft thru, a chain closed
round). Each pulse sent is compared with the source's of the same place: half an interval decides identity, a quarter quality.

## Consequences
- Easier: tempo changes followed without a pulse sent early; a master never taken for an echo where no loop exists.
- Harder: while the source moves, the pulses sent on carry its jitter and the delivery's.
