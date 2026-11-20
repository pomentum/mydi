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
`echo_test.go`, `echo_loop_test.go`, `loopback_test.go`, `restart_test.go` and `held_delivery_test.go` (the recordings in `tests/testdata/`).


<!-- carried from the program mydi began in, reworded: the record as it was decided and measured -->
## Context

A machine makes MIDI clock: 24 pulses a quarter note at a tempo a listener sets. On a stage the master is often a device instead: a drum
machine, a hardware sequencer, a DAW on another machine, and everything else follows it. Today the hub cannot follow: it drops the
clock bytes it hears, because a listener handed 48 messages a second would do nothing useful with them. The owner asked whether the
machine can take its clock from the synth. It can, if the program does the following itself and gives listeners only what they can use. But
a re-made clock is worth having only for some sources: for a hardware master, a MIDI thru box adds next to nothing, and the hub adds its
input's latency, the follower's error, its output thread's tail and its interface's quantisation (the options below).

## Decision

### A. Make or follow

- Any input port the hub may use (`NewHub`'s `allowed`) may be followed; whether anything follows it is the caller's, as making a tempo
  is.
- `Hub.FollowFrom(port, source, FollowOptions{Hold, Meter, Beats, LoopGuard})` makes a port follow a source; `Hub.Clock` with a tempo
  goes back to making one. One port follows at most one source; a source may be followed by several ports.
- **No clock out of the port followed,** whatever the call asks: many synths echo their input to their output (soft thru).
- **An echo is known by the hub's own fingerprint.** Its output's small timing irregularities (a sub-millisecond residual from its own
  grid, pulse by pulse) come back in an echo at a fixed delay, through soft thru or a DIN chain however it started; a real master's do
  not. The follower correlates the input's residuals, on the counter stamps taken in the callback (WinMM's are rounded to the
  millisecond, coarser than the fingerprint), with the hub's own recent output residuals over delays up to 50 ms; input that correlates
  is an echo, refused for lock and counted, before lock and after it. If the rig shows the fingerprint blurred past use, by USB's 1 ms
  frames or by the callback's own delays, a deliberate pattern of sub-millisecond phase offsets on the hub's output is the fallback
  signature.

### B. The input's time

- **The fit uses WinMM's stamps.** WinMM's own stamp for each message (milliseconds since `midiInStart`) is taken outside Go, so Go's
  pauses do not move it; it is mapped onto the time thread's timeline by the performance counter read at `midiInStart`. Its 1 ms
  quantisation (about 0.29 ms RMS) costs little to a line over 24 to 96 pulses.
- The stamp taken in the Go callback (`mydi.Now()`) refines a pulse within its millisecond when the two agree, and is set aside when it
  lags WinMM's: that is the callback held by Go or by the system, the bunching the follower must not learn from.
- **The first check, on the real driver path** (part G step 1): where WinMM's stamp is taken. If the MIDI service stamps on delivery to
  the process rather than on arrival from the device, both stamps bunch together and nothing above detects it; then the stamps of Windows
  MIDI Services or ALSA's sequencer (the input side of ADR-0003 part E) are required before the follower is.
- On Linux, ALSA's sequencer stamps input against a queue in the kernel, which is the stamp to fit on there; a raw MIDI read's return
  carries the reader's wake-up.

### C. The follower

- **A line, not a smoother.** A least-squares line through the times of the source's recent pulses: its slope the interval, so the
  tempo, its intercept the phase, estimated together. A second-order phase-locked loop is the alternative measured against it.
- **Outliers by their place:** a pulse's distance from the line, the line fitted again without those beyond a multiple of the residuals'
  median absolute deviation.
- **Lock** when the residuals' spread is under a tolerance that follows the source's own measured spread, up to a cap, so a jittery source
  (a DAW over USB) still locks. Lock is judged on the pulses received so far, at least 24; the window grows to its full size after lock.
- **A jump** (a song's change, half or double time) shows as a run of outliers on one side, and is re-acquired at the new tempo. **A ramp
  is not a jump:** a straight line lags an accelerando by about half its window and its residuals drift to one side; the ramp case of
  part G must not fire the jump rule, and if it does the line gains a curvature term, or the loop replaces it.
- **Loss** after the larger of four expected pulses and a floor in milliseconds, so the hub's own stalls are not taken for a source that
  stopped; and on Stop. On loss the follower holds the last tempo for `hold` beats, then sends Stop. A source that comes back within the
  hold is re-acquired with the grid slewed to it, never jumped. **A source that comes back after the hold** (Stop already sent on) leaves
  the followers stopped until the master's next Start or Continue: the hub sends no Continue of its own, since it cannot know the song's
  place, and tells listeners the source is back (part E).

### D. The clock sent on

- **The count is kept.** Between Start and Stop, the pulses sent on equal the pulses received. ADR-0002 part B's rule that a stall of
  more than four pulses moves the grid on holds for the hub's own clock, never for a followed one: downstream sequencers count pulses
  for their place in the song (six to a sixteenth), so a pulse dropped moves every follower a tick early for the rest of the song, and the
  hub's own stalls (35-38 ms with notes) are two pulses at 120 BPM. So a stall is caught up with pulses no closer than a quarter interval,
  never skipped; the cross-fade from relayed to re-made pulses neither adds nor drops one; a slew of the phase changes spacing, never the
  count. The one exception is a hold: its pulses are counted, and when the source returns within it, the difference is caught up the same
  way if it is under a beat, and reported (counted, and an event to listeners) if it is not.
- **While acquiring,** each source pulse is relayed one for one, so a master that sends no clock while stopped is followed from its first
  pulse. Once locked, the ports are clocked from the follower's grid, cross-faded within a beat. A source locked before Start (one that
  sends clock while stopped) is re-made from its first pulse.
- **Start and Continue** go out at once; the first pulse after them is the source's first pulse after them, and **never sent before Start
  or Continue has gone out, whatever the offset**: a receiver counts the first pulse after Start as the downbeat's. Continue after Song
  Position is the same. Stop is sent on at once.
- **The offset.** The grid is predicted, so it may be sent early to cancel the input's latency and the output's. The offset is per output
  port, measured by a program's own checks against a loopback on that port, with the rig's figure as a fallback, at most ±20 ms. It is never
  applied in a step: it is brought in gradually after Start or Continue and at lock (relaying cannot send early), as a slew that keeps the
  count.
- **Song Position** is valid only while stopped: one that comes while the source runs is not sent on, and counted.
- **While stopped,** a source that keeps sending clock keeps the followed ports clocked, as receivers that show tempo expect; one that
  sends none leaves them silent.
- The counts of ADR-0003 part D are kept for a followed clock, with the follower's own: lock, tempo, residual spread, pulses received and
  sent (their difference), echoes refused, bunched pulses set aside, losses, holds, re-acquisitions.

### E. What listeners hear

- To the hub's listener (`NewHub`'s `listen`, ADR-0002), events of the type `clock`: `start`, `stop`, `continue`,
  `position` (Song Position, in sixteenths, while stopped), `tempo` (when it moves past a dead band set by the estimate's own variance,
  with hysteresis), `lost`, `back` (a source back after a hold), `count` (pulses received and sent differ), and `beat` or `bar` only when the
  follow asks for them.
- Bars are counted under `meter` (4/4 unless set); a Song Position maps to a bar and beat under it; MIDI clock carries no time signature.
- **Beats and bars are informational.** They reach a listener late by its event loop: a listener must not play notes on them. Timed
  output waits for the shared timeline (part F); a program's help should say so.

### F. Not now

- Ableton Link: its library is GPLv2+ or proprietary and its name a trademark; only with Ableton's licence or a reviewed
  clean-room route.
- MIDI timecode (quarter frames): a film and theatre clock, a follow of its own if a use case asks.
- A listener's events placed on the followed beat ("on the next bar"): after a shared timeline (ADR-0003 part E).

### G. The order, its cases, its criteria and its constants

**No extra hardware** (the owner, 2026-11-14): the goal is a working clock on any machine with an ordinary MIDI port, so the
evidence is what such a machine has. The hub's own measurements (each pulse's lateness against its grid, the longest gap, the grid's
moves: `Hub.Timing`, `mydi timing`, `MYDI_TIMING`), a loopback on the machine's own interface (a cable from its out to its in, a device's
soft thru, or a virtual port for the software path alone), stamped by the operating system's own MIDI time, and a real device or DAW
followed. a program's own checks runs the loopback as a self-test on the user's machine. What this cannot see is the interface's own
buffering below the system's stamps, the blind spot every DAW shares; the ADR says so rather than claim the wire. A logic analyser on
the DIN output (ADR-0003's rig) is optional: more confidence where one exists, never a precondition.

1. **The driver path first:** where WinMM's stamp is taken (on arrival or on delivery), measured with the loopback: the hub's own
   pulses sent at known times and their stamps on return, with the Go callback's stamp beside WinMM's; then stamping and the reader's
   bunching on Windows and Linux.
2. The follower (part C) as plain code, against generated and recorded sources: steady; jittered at ±2 ms, at about 2 ms RMS (a DAW on
   Windows) and E-RM's -38/+22 ms profile; drifting ±0.5%; a ramp, which must not fire the jump rule; a jump past 25%, half and double
   time; dropouts of three and of ten pulses; 30 and 300 BPM; a master that sends no clock while stopped; Start and the first pulse,
   Continue after Song Position, Song Position while running; an echo through soft thru, and one through a DIN chain begun after lock;
   input delayed and bunched; re-acquiring during a hold; a source back after a hold; **the count kept through a stall, the cross-fade and
   a slew.**
3. The re-made clock and the transport (part D) against what is at hand: a hardware master or synth over the interface, a DAW over
   USB and a DAW on another machine, the result read back through the loopback; a per-port offset against it.
4. What listeners hear (part E), through `Hub.FollowFrom`.

**Provisional pass criteria,** written before any data so that results do not move them; the rig may show one unreachable, and then
this ADR says so, rather than the number changing quietly. Phase is measured against the source's pulses as the system stamps them on arrival, not on the wire:

| Criterion | Hardware master (written for DIN; measured over USB, 2026-11-15 to 17) | DAW over USB | DAW on another machine |
|---|---|---|---|
| Pulses lost or added between Start and Stop, excluding a loss of the source | 0 | 0 | 0 |
| First pulse after Start before Start went out | never | never | never |
| Steady tempo error, after lock | under 0.1 BPM | under 0.2 BPM | under 0.2 BPM |
| Phase against the source's pulses (system stamps through the loopback), 99th percentile | under 1 ms | under 2 ms | under 3 ms |
| Output spacing, RMS off the interval | no worse than a thru box plus 0.3 ms | under the source's own | under the source's own |
| `tempo` events on a steady source, per minute | 0 | 0 | 0 |
| Time to lock from the first pulse | under 2 beats | under 4 beats | under 4 beats |
| An echo locked on | never | never | never |

**Provisional constants,** each set by its test:

| Constant | Start | Set by |
|---|---|---|
| The line's window | 24 to 96 pulses | step 2: steady, jittered and ramp cases |
| Outliers beyond | 3 × the residuals' MAD | step 2: jittered and bunched cases |
| Lock tolerance | the source's measured spread, capped at a quarter interval | step 2: the jittered cases lock; step 3 per class |
| A jump after | 6 outliers on one side | step 2: jump, half and double time, and the ramp not firing it |
| Loss after | max(4 pulses, 120 ms) | step 2: dropouts; step 3: the hub's own stalls |
| Hold | 8 beats | step 3: a cable pulled mid-song, with a musician's judgement |
| A hold's difference caught up | under 1 beat | step 2: a source back within the hold |
| Cross-fade | within one beat, the count kept | step 2 and 3: no step in spacing, no pulse lost |
| Offset | per port by a loopback (`mydi loopback`); 0 where none is wired | step 3 |
| Echo correlation | over delays to 50 ms, a threshold from the soft-thru case | step 2 and 3: soft thru and a DIN chain |
| Tempo dead band | from the estimate's variance, with hysteresis | step 2: no `tempo` event on a steady source |

## Options considered

| Option | For | Against |
|---|---|---|
| A hardware MIDI thru box or a thru chain, no computer in the clock's path | Adds next to nothing to a hardware master's clock [Inf] | Needs the outputs on one chain; does not cross machines; does not steady a jittery source |
| Relay each incoming pulse to the outputs as it comes | Simple, no estimate | Every input jitter and the reader's wake-up reach the outputs [Inf] |
| Hand the pulses to listeners | Nothing in the program | 48 messages a second per port through a client, late by its event loop [Inf] |
| A follower in the program that re-makes the clock (chosen, for its cases) | Steadies a jittery source; carries a clock across machines; listeners get transport and tempo | A loop to tune and test; for a hardware master, worse than a thru box |
| Ableton Link | Tempo shared over the network, no cable | Licence; not a MIDI clock |

The recommendation by source: a hardware master over DIN goes through a thru box to the devices that can share its chain, and the hub
follows it only for what it adds (the transport and tempo to listeners, a clock to another machine); a DAW over USB, or on another
machine, is where the re-made clock is the better one, which the rig confirms or refutes per class.

## Consequences

- Easier: a hub can join a rig whose master is a device, steady a jittery master's clock for the devices that follow it, carry it to
  another machine, and let listeners react to the song's transport and tempo.
- Harder: the follower is a control loop with failure modes (lock lost, a jump, an echo, bunched input, a count that differs) that need
  clear behaviour, counts and tests; and it is built on a clock (ADR-0003) not yet measured on the wire.
- Revisit when: the rig shows, for a class of source, that the follower misses its criteria where a relay or a thru box meets them: that
  class is then relayed or left to the thru box, and this ADR says so; or a use case needs MIDI timecode or Link.

## Revisions

- 2026-11-14, first review: the first beat after Start and Start's order against the re-made pulses, a soft-thru loop, a tempo jump never
  learned, an interval smoother in place of a fit, loss tighter than the hub's own stalls, hardware thru left out, constants written as
  decisions.
- 2026-11-14, second review: the pulse count kept (ADR-0002's grid rule does not hold for a followed clock), Start never preceded by an
  early pulse whatever the offset, an echo test that does not refuse a real source, the fit on WinMM's stamps rather than Go's, the pass
  criteria as numbers, a ramp not a jump, a source back after a hold, the offset per port.
- Closed after the second review. Written as closed until a DIN rig produced numbers; part G (the owner, 2026-11-14) took that gate away,
  and what came after is in the action items and the status, not in revisions.

## Action items

- [ ] The DIN rig (a logic analyser, a hardware master, a class-compliant interface, two or three synths): optional, no longer before
  any code (part G, the owner, 2026-11-14).
- [ ] Steps 1 to 4 of part G, each with its cases, against the pass criteria above, setting the constants it names. Their record,
  step by step, is [Following a device's MIDI clock: the record](../reports/following-a-midi-clock.md); the decision it
  changed, while the source moves and for the echo test, is [ADR-0005](0005-a-followed-clock-that-moves.md).
