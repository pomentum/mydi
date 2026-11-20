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

## Context
On Windows, every program's MIDI goes through Windows MIDI Services; a call to send can hold for tens or hundreds of milliseconds while a
device comes or goes, or while a USB device's audio runs beside its MIDI [V-exp]. A hold in the time thread delays every port.

## Decision
**C.** Each port's writer releases what waited by its kind after a hold: a short hold is caught up (every pulse written, closer
together but never closer than a quarter interval), a long one resynchronises the clock past a beat, notes gone stale are dropped, and a
note off or All Notes Off is never dropped.

**D.** A call that has not returned after 20 s (`SetStuckAfter`), or a port whose writer's ring of 4,096 writes is full, is given up:
the writer is abandoned, the port closed, and opened again with a new writer when `Hub.Revive` or the next use finds it. Closing the hub
gives up a writer still held after a second.

**F.** Above-normal priority class for the process, so the runtime's own threads are served.

## Consequences
- Easier: one device's trouble is that device's alone.
- Harder: a thread per port, each at the time thread's priority.
- Revisit when: Windows MIDI Services no longer holds sends across devices.
