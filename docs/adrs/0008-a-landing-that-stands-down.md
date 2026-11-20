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


<!-- carried from the program mydi began in, reworded: the record as it was decided and measured -->
## Context

A receiver reads tempo from the time between the pulses it gets, so how close each pulse lands to its time is what its tempo display
shows. The writer wakes on its timer and writes, about 0.3 ms off on average. A landing wakes it earlier and has it wait the last stretch
on the counter. Through a synth's clock send, every pulse back in every run:

| | At the hub, 99th percentile | Landed late (of 5,760) | The synth's display |
|---|---|---|---|
| No landing, idle | 0.73 ms | — | ±0.1 BPM |
| 1 ms, idle | 0.33 ms | 10 | steady, with stretches of ±0.1 |
| No landing, every core busy | 1.01 ms | — | ±0.1 BPM, never steady |
| 1 ms, every core busy | 1.41 ms | 322 | ±0.5 BPM |

So the landing is what a steady display needs on a quiet machine, and the worst choice on a loaded one: woken in time, the writer is taken
off the processor while it waits, for longer than the landing saves [Inf: MMCSS takes a registered thread down for its reserve to the rest
of the system once it runs long, and waiting on the counter is running]. A fixed landing cannot serve both; the writer already counts,
write by write, whether it landed cleanly.

## Decision

### A. Landing on by default

Each port's writer lands its timed writes (a clock's pulse, a scheduled event, a following's queued write), 1 ms, the setting that held
the display still. A write for at once is written on reaching it, as now.

### B. Standing down

The writer keeps its last 96 timed writes' outcome (a bar of pulses at 24 a beat): landed, landed late (woken before its time, written
more than 0.3 ms after it) or woken late (woken after its time). When more than 2 of them are late, it stands down: it writes on waking,
with no wait on the counter, for 192 timed writes (2 bars of a clock's pulses, 4 seconds at 120 BPM). Then it lands again, and its window
starts empty. A stand-down that comes within 96 writes of the last one's end lasts twice as long as that one, up to 1,536 writes (16 bars,
32 seconds at 120 BPM); one that comes later starts again at 192. A try that fails costs at most three late writes before it stands down
again, so it tries often: first proposed at 8 bars doubling to 64, it outlasted 30 quiet seconds after 30 loaded ones. So a machine that stays loaded has its writer standing down nearly all the time and costs no
more than no landing; a machine that goes quiet has its landing back within seconds.

Each port decides alone: a port whose driver holds writes (ADR-0006) does not stand down the others.

### C. Counted

The writer counts its writes landed, landed late and woken late (already), and now its stand-downs and the time spent standing down,
in the hub's timing per port, so a program can say whether the machine let the clock land.

## Options considered

| Option | For | Against |
|---|---|---|
| No landing (now) | No cost under load | The display flickers ±0.1 BPM on any machine |
| A fixed landing | Steady display idle | ±0.5 BPM under load, worse than none |
| A shorter landing (0.5 ms) | Less waiting on the counter | Half the gain idle, measured; still waits under load |
| A landing sized by the writer's wake jitter | Follows the machine | The jitter barely moves with load (0.32 to 0.50 ms): it does not see the cost, which is being taken off mid-wait |
| **Stand down on landing late (chosen)** | Measures the cost itself, write by write; no landing's behaviour under load, the landing's idle | A loaded machine still pays one window of late writes before it stands down, and one at each try |

## Consequences

- Easier: a steady tempo on a receiver's display on a quiet machine, with no setting; under load, no worse than today.
- Harder: the writer's state has one more mode, with constants (2 of 96, 2 to 16 bars) that the measures below set.
- Revisit when: a machine loaded in bursts flaps between the two (the stand-downs' count shows it; 22 stand-downs in five idle minutes
  as a service, 2026-11-17, and 38 in ten as a desktop process the next night; that night's desktop A/B: 31 stand-downs in five minutes,
  0.96 ms at the 99th percentile with the landing against 0.62 ms without it, so the condition was met there; the service half of the
  A/B is open, the clock report's "As a service"); or the stand-down's tries cost a
  receiver a visible jump.

## Action items

- [x] Built, with the suite's tests (`tests/lander_test.go`): a lander that lands late stands down and comes back, the window, the
  doubling and its cap, and the writers landing by default; each port alone by construction (a lander per writer), not tested apart.
- [x] Measured through the synth's clock send (2026-11-16): idle 0.31 ms at the 99th percentile, never standing down (the 1 ms landing: 0.33);
  every core busy 0.76 ms, standing down after 141 writes (no landing: 1.01; the fixed landing: 1.41); with the load switched, the landing
  back in the quiet stretches (0.39, 0.52; again 0.27, 0.58, 0.49) once the stand-down was shortened to 2 bars doubling to 16; the display steady idle and ±0.1
  BPM busy (the owner). Every pulse back in every run but one, which lost 5 in a hold of Windows' write call after a load ended (the
  report's open finding).
- [x] Writes held 30 to 180 ms, once 732 ms (not the load ending, the series showed; the pulses that seemed lost most likely the synth's
  clock send handling a long hold, the second hour showed). Partly done: the synth's USB set to MIDI alone (2026-11-17, four runs of
  three minutes: one run in four held a write past 30 ms, the longest 120 ms, against 6 in 11 up to 177 ms with audio; few runs), and
  an hour with MIDI alone (2026-11-17: one hold, 117 ms; 20 pulses past 5 ms, all in it; 1.24% past 1 ms, idle, most in its first two
  minutes), and the same hour loaded with the program and a client (the longest gap 138 ms; past 5 ms 0.03% quiet, 0.01% loaded, against 2.15%
  loaded with audio). A trace stopped
  just after a hold is not pursued (the owner, 2026-11-17: no Windows Performance Recorder): with the synth's USB set to MIDI alone the holds stayed under the cliff for an idle and a loaded hour; the holds' cause stays unidentified (the clock report's known limitation).
