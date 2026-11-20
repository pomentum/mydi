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


<!-- carried from the program mydi began in, reworded: the record as it was decided and measured -->
## Context

ADR-0004 re-made a followed clock from a line fitted through the source's pulses once locked: steady, it is far better than relaying
(a phase 99th percentile of 0.24 ms against a relay's 1.0, spacing 0.02 ms RMS against 0.58, on generated sources). Its first runs on a
hardware synth found what generated sources had not, and a review asked for the designs compared, each pulse against the source's own
pulse of its place, rather than the constants tuned:

- **A run went a pulse behind for its whole length and kept its count.** The synth sends its clock while stopped and restarts it at
  Start, its first pulse 0.6 to 19 ms after its last one stopped; where that fell in a slot the line had filled, the echo test took it
  for an extra and refused it. A count kept is not enough.
- **A tempo that changes was the weakness, not jitter.** The line keeps the tempo the source has left until six outliers decide a jump
  or a ramp: a jump lost the count (the follower renumbered its pulses), a tempo turned within a beat sent pulses up to 135 ms off, the
  synth's tempo turned by hand put the 99th percentile at 13.7 ms, and a slow ramp lagged by half the line's window, 2.7 to 3.8 ms,
  where a relay keeps 0.3.
- **The echo test caused both defects on a real device**, against a synth with no loop anywhere: every pulse it refused there was the
  source's own. A loop (a synth's soft thru, a DIN chain closed round) is real, and only the person who wires it knows it is there.

## Decision

### A. Re-made while steady, the source's own pulses while it moves

- **Steady,** the clock is re-made on the line, as ADR-0004 part D says.
- **The source is moving** when two pulses in a row lie off the line on one side (beyond the outlier threshold), until two pulses in a
  row are back on it; or when the mean residual of the last beat's pulses on the line (24) is past 4% of the interval, or the line's own
  spread if larger, until it falls under half that: a slow ramp the line lags, though no pulse is an outlier.
- **While it moves,** each pulse goes out when the source's pulse of its place came, and none is sent ahead of the source on a tempo it
  has left. **After a single late pulse** the next is waited for: if it comes on time it comes at its place anyway. **A single early
  pulse** is still sent on the line, so a DAW's spike is smoothed, at the cost of a pulse up to a quarter interval late where a source
  speeds up before a second pulse confirms it.
- **A ramp is refitted through its own run of pulses,** not beside pulses of the tempo the source left, which held the line behind and
  widened its spread until pulses 20 ms off counted as on it. **A jump keeps the pulses' places in the source's count,** so the count
  sent since Start is kept across it.
- **The first pulse after Start or Continue is the source's own:** refused only as an echo the test has proven (part B); and where it
  lies off the line, the line moves to it, its interval kept, since a device may restart its clock at Start.
- **A source too uneven to re-make** (its line's spread past a sixth of the interval, `UnevenSpread`: a synth under 30 BPM sent pulses
  83, 84, 83, 83 and 167 ms apart) is moving too: its own pulses are sent on, each at its place, never a line that fits none of them.
- **No pulse is counted missing while the source moves, drifts or is uneven:** a dropout is told by a gap of more than 2.5 of the
  line's intervals, which a line a step behind a slowing source, or an uneven source's double gap, also makes.
- **A followed port that goes and comes back** (a cable pulled) has its input closed while it is gone and opened again when it is back;
  the follower holds, stops the followers past its hold, and takes the device's next Start, as for any source silent.
- The count of ADR-0004 part D is unchanged: between Start and Stop the pulses sent on equal the pulses received.

### B. The echo test, a loop guard

- **Off by default.** It is turned on by the follow: `FollowOptions.LoopGuard`, `--loop-guard` on `mydi
  follow`, where the hub's own clock can come back into the port followed (a synth's soft thru from a port the hub clocks, a DIN chain
  closed round).
- **No clock out of the port followed** stays, whatever the follow asks (ADR-0004 part A).
- With the guard on, it refuses only what it has evidence for: a moving source's pulses and the first after Start are refused only as a
  proven echo, since the line's slots are not the source's then.

### C. The criteria

Two rows join ADR-0004 part G's table, each pulse sent after Start against the source's pulse of the same place, every pulse: **within
half an interval** in every column (identity: past it the pulse stands for another), and **within a quarter interval** in every column
(quality). No number of part G moves. A measured column records each run on hardware: a hardware master over USB, which no column
names, read against the hardware column.

| Criterion | Hardware master over DIN | DAW over USB | DAW on another machine | Measured: a hardware synth over USB on Windows, into a software port (2026-11-15) |
|---|---|---|---|---|
| Pulses lost or added between Start and Stop, excluding a loss of the source | 0 | 0 | 0 | 0 in nine spans after the fixes; before them, a whole run a pulse behind with its count kept |
| First pulse after Start before Start went out | never | never | never | never, in eight Starts |
| Each pulse against the source's of its place, every pulse (identity) | within half an interval | within half an interval | within half an interval | at most 7.7 ms at 120 BPM (10.4 ms), met |
| Each pulse against the source's of its place, every pulse (quality) | within a quarter interval | within a quarter interval | within a quarter interval | steady: at most 3.3 ms, met; the tempo turned from 120 to 142 and down to 75 BPM: one pulse at 7.7 ms, missed |
| Steady tempo error, after lock | under 0.1 BPM | under 0.2 BPM | under 0.2 BPM | 120.00 ± 0.01 BPM at a displayed 120, met |
| Phase against the source's pulses, 99th percentile | under 1 ms | under 2 ms | under 3 ms | 2.3 to 2.5 ms, missed against the hardware column: no loopback, so against the source's stamps, the system's own delivery a median of 1.6 ms of it |
| Output spacing, RMS off the interval | no worse than a thru box plus 0.3 ms | under the source's own | under the source's own | 0.32 to 0.60 ms against the source's 0.70 to 0.87, under the source's own; no thru box compared |
| `tempo` events on a steady source, per minute | 0 | 0 | 0 | not built (part E of ADR-0004) |
| Time to lock from the first pulse | under 2 beats | under 4 beats | under 4 beats | not recorded: the synth clocks while stopped, locked before Start |
| An echo locked on | never | never | never | not tested: no loop in this setup |

The generated cases hold every row they can (the record's tables): the count kept and each pulse within half an interval in every case;
a quarter interval missed by single pulses where a source speeds up within a beat, at most 6.0 ms going to 100 BPM.

## Options considered

| Option | For | Against |
|---|---|---|
| Relay every pulse (ADR-0004 rejected it) | Phase within about 1 ms in every case, a slow ramp included | Passes on the source's jitter (0.58 ms RMS); no smoothing for a jittery DAW |
| The line alone, re-made once locked (ADR-0004) | Steady: phase p99 0.24 ms, spacing 0.02 ms | Lags every tempo change; lost the count on a jump; 135 ms on a tempo turned within a beat |
| **The line while steady, the source's own pulses while it moves (chosen)** | Steady as the line; through jumps, turns and slow ramps near a relay (p99 0.8 to 2.2 ms, generated) | Spacing during a slow ramp the source's own (0.55 ms RMS against the line's 0.2) |
| A curvature term in the fit | Could keep both phase and spacing in a ramp | Not needed to meet the criteria; noisier when steady [Inf] |
| A second-order loop in place of the line | The usual design | Lags a ramp the same way [Inf]; not built, compared only if the line misses |
| The echo test on by default (ADR-0004) | Safe against a loop nobody declared | Misfired on its first real device, twice |
| The echo test on only where the hub clocks the device it follows | Automatic | A DIN chain through other devices is not seen |
| No clock out of the followed device, matched by its name's stem | Catches a device whose input and output are named apart | Not needed for the common case: a port is a device's name, its input and output one port (`drum-machine`, in and out), so following it already refuses clocking it (`TestAPortFollowedIsNeverClocked`); a device with several ports, or one named apart, is the gap, taken with the loop guard |

## Consequences

- Easier: a followed clock keeps its place through a tempo turned by hand, a jump and a ramp; a source with no loop is never refused.
- Harder: the clock sent on switches between two behaviours, and the switch is a rule with constants (two pulses, a beat, 4%) that
  the hardware cases still have to confirm; a loop that nobody declares is not guarded.
- Revisit when: a jittery source moves the follower into following it while steady (its spikes passed on); a slow ramp's spacing
  matters to a receiver; a source is uneven enough that its own pulses are sent on (`UnevenSpread`, a sixth of the interval: a synth's
  clock under 30 BPM) where an even clock at its average would serve its receivers better; or a loop is found that a person could not
  have known to declare.

## Action items

- [ ] The cases left (the record's last section): the synth under load, the cable pulled, after idle, Ableton's baseline; a real
  receiver; a soft-thru loop with the guard on, on hardware.
- [x] `FollowOptions.LoopGuard`, with step 4 of ADR-0004 part G (2026-11-16): applied
  at once while the source is stopped, else at its next Start or Continue, since the pulses the test holds aside are part of the count.
- [ ] A port that sends clock and no Start or Stop (a device's transport send off) said so.
- [x] A gap past the dropout rule decided by the pulse after it (2026-11-16): one on time means pulses were missing, one in a burst a
  delivery the system held; part A's rule told a held delivery of 2.8 intervals for two pulses missing and put a synth's count two
  ahead ([F14 again](../reports/following-a-midi-clock.md#f14-again-under-adr-0007-2026-11-16)). ADR-0004 part D's count is what it keeps.
