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

## Context
A MIDI clock is 24 pulses a quarter note: at 120 BPM one every 20.8 ms. A receiver hears every pulse's spacing; a pulse a few
milliseconds late is audible as a tempo wobble in a synth that follows it. Go's timers, the scheduler and the collector each add
milliseconds on Windows under load [V-exp].

## Decision
**A.** One goroutine, locked to its OS thread, holds a heap of jobs on one timeline: the clocks' next pulses (one job each, reused) and
the timed events. It allocates nothing per pulse. It never makes a driver call: it hands each write to the port's writer.

**B.** On Windows the timeline is the performance counter and the wait a high-resolution waitable timer; elsewhere Go's monotonic clock
and timer. Nothing on the path reads the wall clock.

**C.** On Windows the thread joins MMCSS's "Pro Audio" task at critical priority, or runs at `TIME_CRITICAL` where MMCSS refuses; the
process opts out of power throttling and runs above normal. The lead aims the timer early by a slowly moving estimate of its wake-up
delay; measured through a synth it did not help, so it is off unless `SetLead(true)`.

**D.** `Hub.Timing` answers the pulses written, those more than 1 and 5 ms late, the longest gap, the grid's moves and each port's
writer's counts, so a program can show how well it kept time.

## Consequences
- Easier: a clock that keeps its grid under load, and numbers that say when it did not.
- Harder: one more thread at a high priority; it must never block (hence the writers).
- Revisit when: Windows MIDI Services' scheduled send is generally available, or Linux needs `SCHED_FIFO` (part E).
