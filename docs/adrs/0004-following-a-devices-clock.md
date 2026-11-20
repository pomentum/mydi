# ADR-0004: Following a device's MIDI clock

| Part | Decision | Status |
|---|---|---|
| A | A port's clock either makes its own tempo ([ADR-0002](0002-events-ports-and-held-notes.md) part B) or follows the clock a device plays into another port (`Hub.FollowFrom`); no clock goes out of the port being followed; input that carries the hub's own timing fingerprint is its echo and never locked on | Accepted; amended by ADR-0005 |
| B | The follower fits on the system's own input stamps (WinMM's, taken outside Go), mapped onto the time thread's timeline; the stamp taken in the Go callback only refines them; whether a driver stamps on arrival is checked first, by a loopback | Accepted |
| C | A robust line through the source's recent pulse times gives tempo and phase; outliers judged by their distance from it; a sustained jump re-acquired, a ramp never taken for one; loss after the larger of four pulses or a floor | Accepted; amended by ADR-0005 |
| D | Between Start and Stop the pulses sent on equal the pulses received: a stall is caught up, never skipped; relayed while acquiring, re-made once locked; the first pulse after Start or Continue never before Start has gone out | Accepted; amended by ADR-0005 |
| E | Listeners hear the transport and the tempo, never the pulses; beats and bars under a stated meter, informational only (`FollowOptions`) | Accepted |
| F | Not now: Ableton Link, MIDI timecode, events placed on the followed beat | Accepted |
| G | No extra hardware: the hub's own measurements and a loopback on the machine's own interface are the evidence; the cases, their pass criteria and the constants each sets (`DefaultFollow`, `DefaultEcho`) | Accepted; amended by ADR-0005 |

**Status:** Accepted, 2026-11-20.
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** [following a MIDI clock](../reports/following-a-midi-clock.md) (the follower's cases, recorded devices, the loopback) and
[the Windows report](../reports/midi-clock-on-windows.md) [V-exp]; `follow_test.go`, `follow_from_test.go`, `remake_test.go`,
`echo_test.go`, `echo_loop_test.go`, `loopback_test.go`, `restart_test.go` and `held_delivery_test.go` (the recordings in `testdata/`).

## Context
A drum machine or a sequencer is often the band's clock. A program that plays with it must take its tempo and phase and send that clock
on to other devices without losing a pulse, since a receiver counts pulses to know where the bar is.

## Decision
**A to D** as the table says. The follower (`Follower`) is plain code with no port and no clock: pulse times in, tempo and phase out, so
it is tested on recorded and generated sources. The re-maker (`Remaker`) turns its estimate into the pulses sent on, counted against
those received. `Hub.FollowFrom` runs both on the time thread for a port; `Hub.Follow` follows one port into several and is what
`cmd/mydi follow` runs.

**E.** What a following tells its listeners are `Event`s of type `clock`: `start`, `stop`, `continue`, `position`, `tempo` (when it moves
past the estimate's own band), `lost`, `back`, `count` (when the pulses sent and heard differ), and `bar` or `beat` when asked under a
meter. They are late by the listener's own loop and never drive notes.

**G.** The pass criteria and constants are in the report's table and in `DefaultFollow`, `DefaultEcho`, `DefaultLoopback`.

## Consequences
- Easier: a program plays in time with a device's clock, and says when it lost it.
- Harder: a source that moves needs ADR-0005's rules; a system that stamps on delivery needs another stamp (part B).
- Revisit when: Link or MTC is asked for (part F).
