# Following a device's MIDI clock: the record

**Question.** ADR-0004 decided how a hub follows a device's MIDI clock and set the order of its evidence (its part G): the driver
path first, the follower and the re-made clock as plain code, then a real device. This is that record, step by step, moved here from
the ADR's action items once it outgrew them; [ADR-0005](../adrs/0005-a-followed-clock-that-moves.md) is the decision it changed.
**Evidence labels** as in [ADR-0000](../adrs/0000-record-architecture-decisions.md): **[V-exp]** observed on the machine and in the run each entry names (a
Windows 11 desktop; a hardware synth's own clock over USB; the hub's output into the Microsoft GS Wavetable Synth, a software port);
**[V-code]** read in the package; **[Inf]** inference. The generated cases are the tests named, run in the
pinned Go image.

## The record, step by step

- Step 1's tool (2026-11-14): Windows input read with both stamps, WinMM's mapped onto the timeline by the counter read around
  `midiInStart` and the callback's on entry (`OpenInStamped`); `mydi loopback --out <id> --in <id>` sends the hub's pulses
  through the loop, holds its own read now and then so the system must queue what arrives, and says from the bursts whether the
  system stamps on arrival or on delivery, with the loop's delay, the read's lag and the stamps' spacing; tested on a stand-in loop
  stamping each way, and one never held as the negative control (`loopback_test.go`). Linux's raw MIDI gives no stamp of
  its own; its sequencer's is not read yet.
- Step 1's result (2026-11-15) [V-exp: a hardware synth's own clock at about 120 BPM over USB, on a Windows 11 desktop]: **WinMM's
  stamp is taken upstream of delivery to the process** (in the MIDI service or the driver), not on delivery: the failure part B
  feared is ruled out. Holding the hub's own read cannot show whether the service itself stamps late under load; a run with every
  core busy, the stamps still at the source's spacing, would make "on arrival" fair for practice, and is next. The synth echoes nothing of its USB input, so the loop was not possible; `midi loopback --in <id>` with
  no `--out` listens to a device's own clock instead, holding the read the same way and judging each burst against the source's own
  spacing (its span over the pulses). In two runs of 480 pulses, with the read held 100 ms every 24 pulses, 76 pulses came in bursts
  each time, the reads up to 81 ms behind, and the system stamped all 76 at the source's spacing within its millisecond, none bunched;
  every stamp's spacing within 1 ms of the source's. The first run, with the read held 30 ms, gave no verdict: at 120 BPM a 30 ms
  hold queues one pulse, and a burst needs two in a row, so the tool's default hold is 100 ms now (the stand-in's 5 ms pulses had hidden
  it). Part B holds on Windows: the follower fits on WinMM's stamps. Not measured: the loop's own delay, which needs a loop or a
  device that echoes, and which the program's `doctor`'s per-port offset needs in step 3.
- Step 2, part C (2026-11-14): the follower as plain code (`follow.go`), a least-squares line through the source's
  pulses indexed by their place in its count, outliers beyond three times the residuals' scaled median absolute deviation or a tenth
  of an interval, lock on 24 pulses within a quarter interval, loss after the larger of four intervals and 120 ms, a hold of eight
  beats, then lost. Tested on generated sources (`follow_test.go`): steady at 30, 120 and 300 BPM, jittered ±2 ms, 2 ms RMS,
  a DAW's spikes (one pulse in twenty moved -38 to +22 ms), drifting 0.5%, each over twenty seeds (500 run once): locked by the 24th
  pulse, under part G's two and four beats, tempo within 0.1 and 0.2 BPM; ramps up and down never taken for a jump; jumps of 33%,
  half and double time re-acquired within two beats; dropouts of three bridged and of ten held and taken again; a source past the hold
  lost and acquired again from nothing; bunched input set aside. Three rules the cases set, beyond part G's table: a run of six
  outliers on one side is a jump only when its own interval is more than 10% off the line's, otherwise a ramp the line catches up
  with; a pulse counts the ones before it as missing only when it comes more than 2.5 intervals both after the last pulse and after
  the line's place for it; and only once locked.
- Step 2, part D (2026-11-14): the clock sent on as plain code (`remake.go`), asked by the time thread what is due:
  Start and Continue at once, the first pulse after them the source's own and never before them; relayed one for one while acquiring
  and the line's once locked; a stall caught up no closer than a quarter interval; the offset brought in by an eighth of an interval
  a pulse; Song Position only while stopped; a source lost held eight beats, then Stop; one back within the hold with the count
  taken from where it is. Tested (`remake_test.go`): the count kept from Start to Stop for steady, jittered and spiky
  sources over twenty seeds (500 run once), through 40 ms stalls every two beats, with a 10 ms offset slewed in, after Continue, from
  a source that clocks while stopped, and across a cable pulled for ten pulses; a jittered master (2.93 ms RMS spacing) sent on at 0.44
  ms. **A finding the criteria did not foresee:** a line's pulse is due before the source's pulse it stands for, so a Stop that comes
  later than the source's next pulse would have (a DAW's Stop on a late pulse), or any early offset, finds one pulse already sent: no
  clock can know a Stop in advance. The re-made clock therefore waits for the source's pulse up to a tolerance past its place (three
  times the source's spread, at least 1 ms, at most a quarter interval) unless an offset sends early, which keeps the count whenever the
  Stop comes in place of the next pulse; a later Stop adds one pulse, counted (`Added`) and to be told to listeners as `count`, never
  hidden. Part G's "0 added" holds only for a Stop in the next pulse's place and no early offset, and step 3 measures how often a real
  master's Stop is later. Waiting costs steadiness on late pulses: the 0.44 ms above is 0.09 ms without the wait.
- Step 2, part A's echo test (2026-11-14, `echo.go`, wired into `remake.go`): input pulses paired with the pulses the
  machine wrote (the times it wrote at, told by `Wrote`, never the times it aimed for, which lie on a smooth line with no fingerprint) at
  each delay from 0.3 to 50 ms, measured over the trailing run of pairs; an echo when the spacing irregularities of single steps
  correlate above 0.7, the delays hold within 0.6 ms, and the pairs take at least 80% of the hub's pulses across the run, decided on
  48 pairs and checked again every beat; the dither (±0.5 ms) as the fallback signature. Tested on generated timings
  (`echo_test.go`), over 300 seeds: a soft-thru echo of the hub's own clock known by its 24th to 48th pulse, none taken
  as real before; a real master the hub follows never refused, under 0.3% of its pulses held a moment; a chain closed after lock
  refused pulse by pulse; through USB's millisecond rounding the natural fingerprint is lost (correlation -0.04) and the dither
  restores it (0.79). In the clock sent on (`echo_loop_test.go`): the hub's own clock coming back through a synth, with no
  master, is never followed and nothing is sent on; a master followed through a chain closed after lock keeps its count in every one
  of 1,000 seeds, with every echo refused, and the test fails on any seed that misses it. Three rules closed the misses: a pulse held
  while acquiring blocks the lock until it is settled, and a held pulse learned late is put in its place by time, the count moved on
  with it; a pulse not yet judged, locked and in an empty slot, is the master's, and in a slot already filled an echo; a write is
  paired with one input pulse, the one nearest the delay, so a master's pulse near an echo's adds no second pair that breaks the
  echo's evidence. Each wrong turn on the way was a statistic chosen by its own pairs (pairs kept for agreeing correlate
  whatever the source; a few gaps dominate a correlation; one chance pair long before stretches a span; three spacings are too few to
  judge), and each rule it left is in the code's comments. A pulse with no evidence yet (fewer than eight spacings at its delay) is
  decided by the line: on it, or late in the next empty slot, the master's; an extra in a slot already heard, an echo; with no line
  yet, held. Limits: an echo closer to the master's own pulse than the line's outlier threshold (about 2 ms at 120 BPM), or later
  than half an interval, is not told apart by timing. Not yet: steps 3 (the re-made clock on the time thread,
  against a device or a DAW, and the program's `doctor`'s per-port offset) and 4.
- The first beat after Start (2026-11-15): a master that sends no clock while stopped, followed with the echo test on and no loop,
  had its first beat sent up to a third of a second late. Each pulse after the first pairs with the hub's relay of the one before, at
  the source's own interval, so it has no evidence either way; held until there was some, it stopped the very writes the evidence
  needed, and the held pulses were decided by the time-out. Now an even run of four pulses or more in a row is the master's: an echo
  is an extra inside one of the master's intervals, splitting it into d and I-d, and with the next spacing I one of the three is at
  least I/3 from their median, never within the quarter the run allows; a held pulse jittered off the run's start is the master's
  when the pulses between the last one learned and the run are as many as the master's spacing puts there, an echo being one more.
  It counts only while every pulse the hub wrote within the echo test's reach was the re-maker's own relay: another clock of the
  hub's coming back is even too. Pulses learned late are caught up at once, a quarter interval apart. The tolerance was an eighth at
  first and widened to a quarter after a jittered case failed, with the I/3 argument checked before it was. Measured over 1,000
  generated seeds each [V-exp, `TestAMasterSilentWhileStoppedIsSentOnFromItsFirstBeatWithTheEchoTestOn`]: a clean source's latest
  pulse of the first beat two intervals late, as designed (the second pulse waits for the fourth); with 0.5 ms of jitter, at most 2.10
  intervals at 120 BPM (43.8 ms) and 5.09 at 240 BPM (53.0 ms, p99 4.05), the pulses after the first beat within a quarter interval;
  the count kept and no pulse before Start in every seed. The test's bounds are the design's (two intervals clean; within the echo
  test's eight spacings jittered, never the time-out); the milliseconds are measured, not a criterion. A first beat on time with no
  wait at all needs the echo test off, which is right where nothing the hub clocks can come back into the port followed.
- Freeze and failure cases (2026-11-15), each a test of the package: a flood of pulses (a loop feeding itself, a broken device)
  never runs a followed port faster than a quarter interval or 2 ms, the larger; the floor had followed the flood down when the
  follower took it for a jump to a tiny interval, which the test found, so it never falls under a quarter interval at 300 BPM; pulses
  relayed while stopped that come sooner are dropped and counted (`RelaysDropped`), and counted ones are caught up at that spacing
  (`TestAFloodNeverRunsTheOutputFasterThanAQuarterInterval`). Start with no pulse sends Start alone and waits, since a source never
  fitted is never lost (`TestAStartWithNoPulseSendsStartAloneAndWaits`). Output stalls of 100 and 300 ms are caught up, the count
  kept, never closer than a quarter interval (`TestLongStallsAreCaughtUpNeverSkipped`). The port and time-thread cases are ADR-0003's.
  Not taken: note-offs on the transport's Stop, since the clock owns no notes and a listener's note may rightly outlive a Stop; the
  hub releases a client's notes when the client stops, its link closes, a port comes back or the program ends (ADR-0002 part C).
- Step 3's wiring (2026-11-15): `Hub.Follow` (`following.go`) reads the followed port with the system's stamps, each
  refined by the callback's own when the two agree within the system's millisecond and taken at the middle of it when the callback
  lagged (part B, counted in the report); hands the transport and the clock to the re-maker and anything else to the listeners; and
  puts the re-maker on the time thread (ADR-0003): one reused job of its own asks what is due, aimed at the re-maker's next due time
  (`NextDue`, with the follower's `CheckAt` for a loss or a hold's end) and again whenever a message comes, so it never polls; what is
  due is written in the same turn to every followed port, a pulse kept a quarter interval after the last at its own time, and the
  echo test told when each pulse was written. A port followed is never clocked (part A); stopping it sends Stop when the transport
  runs. Its report: the counts, the lock and tempo, each pulse's phase against the source's of the same place since Start, both
  sides' spacing, the input's delivery after the system's stamp, all again from the second beat on. `mydi follow --in
  <id> --out <id>` runs it on the machine's own ports. Tested on stand-in ports (`TestTheHubFollowsAPortsClockAndSendsItOnWithItsCountKept`:
  Start, four beats at 240 BPM and Stop, the count kept, one Start, no pulse before it).
- Step 3 on Windows's own time thread, stand-in ports, 2026-11-15 [V-exp: the test built for Windows and run natively on a 16-core
  Windows 11 desktop, the source a goroutine of the test's process stamping each pulse at its own time]: idle, seven runs of eight
  gave from the second beat a phase median of 0.22 to 0.71 ms and p99 of 1.4 to 2.4 ms, the spacing sent 0.25 to 0.57 ms RMS against
  the source's 0.52 to 0.73, steadier than it came, the count kept. The first run after each build stalled the whole process about
  200 ms (an in-memory write took 193 ms, the delivery held as long) [Inf: the system looking at a new executable on its first run]: a
  machine freshly installed meets it once. Two hops on the time thread and a quarter millisecond's pause after a message had put about a
  millisecond on the phase; writing what is due in the same turn took it off. Against part G: the DAW's column (p99 under 2 ms) is
  met idle in most runs; the hardware master's (under 1 ms) is not, the input's own delivery (a median of about 0.9 ms in the stand-in)
  being in the phase until the per-port offset sends early. Under load the stand-in measures itself as much as the hub: with every core
  busy at normal priority its source goroutine was starved, pulses delivered up to 3.5 s after their stamps, and with the collector at
  200 MB/s (500 times a program's own rate, ADR-0003) about 100 ms; the follower took a delivery held past its loss floor for a lost
  source, held the line, and past the hold sent Stop, so the count was lost in four runs of five. That is the hold working as written,
  and it shows an open question: a delivery held longer than the hold stops the followers though the stamps show the source went on,
  and inside the process a starved delivery and a pulled cable look the same. A device's pulses are stamped by the system and only
  their delivery is held, so the measure under load is the synth's (F14), next; the hold's answer waits for it.
- Step 3 with a hardware synth as master, idle, 2026-11-15 [V-exp: the synth's own clock over USB at 120 BPM into the Microsoft GS
  Wavetable Synth, on the Windows 11 desktop]: the synth sends its clock while stopped (part A's case), and Start sends Start and its
  first pulse in the same millisecond, its clock restarted: that pulse came 0.6 to 19 ms after its last pulse stopped, in eight
  Starts. Where it came early in the interval (4 and 6 ms) it fell in a slot the line had filled while stopped, the echo test's
  rule for a locked line took it for an extra, and it was refused: the next pulse counted as the first, and every pulse sent on was
  one behind the synth's for the whole run (a phase of about 22 ms in the report), though the counts sent and heard matched. Fixed:
  the first pulse after Start or Continue is the source's own, refused only as an echo the test has proven, since the hub has
  written none since; and the follower moves its line to that pulse when it lies off the line, interval and spread kept
  (`Follower.Restarted`), so the line does not keep the phase it learned while stopped. `midi follow --record <file>` writes every
  clock and transport message heard and written; a recording of the synth's three Starts (4, 6 and 14 ms) is a test source
  (`tests/testdata/drum-machine-start-stop.txt`, `TestADrumMachinesRecordedStartsAreEachFollowedFromTheirFirstPulse`), with a
  generated device restarting 2 to 18 ms after its last pulse (`TestADeviceThatRestartsItsClockAtStartIsFollowedFromItsFirstPulse`);
  both failed before the fix. On the synth after it: four Starts (0.6, 6, 10 and 19 ms), each with the count kept, none refused, the
  phase from the second beat a median of 1.2 to 1.3 ms, the input's delivery after the system's stamp a median of 1.2 ms, the
  spacing sent 0.60 ms RMS against the synth's 0.87. The worst single pulses were 8.6 and 21 ms off: one where the synth's own spacing
  held 31 ms, and, in the recording, a second where the synth's spacing ran about 5% fast then slow for a second and the line kept
  its interval for the six outliers a ramp needs, its pulses going out up to 12 ms early. A source that changes tempo meets the same
  lag (a 99th percentile of 14 ms in a run with the tempo turned by hand); how fast the line takes a ramp is open. Also seen: the
  synth sends no Start or Stop unless its transport send is on, and then the follower locks on its clock and counts nothing.
- Tempo changes, 2026-11-15. A review of the synth run asked for the designs compared on recorded and generated sources rather than
  the constants tuned: each pulse sent against the source's own pulse of the same place, since a count kept is not enough (the run a
  pulse behind kept its count). Compared on generated sources (`TestATempoChangeIsFollowedAndComparedWithARelay`: steady, ramps of
  20 BPM over eight bars, a tempo turned within a beat, 5% up and back, jumps to 140 and 90 BPM; ten seeds each, 0.3 ms of jitter, on
  the system's millisecond stamps; phase against the source's true times): a plain relay keeps every pulse within about 1 ms and
  passes on the source's jitter (0.58 ms RMS); the line, steady, is far better (p99 0.24 ms, 0.02 ms RMS); but as built it lost the
  count on a jump in every seed with the echo test on (refused pulse by pulse, the jump never learned), and on a jump without it its
  pulses ran up to 40 ms off and a tempo turned within a beat up to 135 ms. Three faults, each fixed: the follower renumbered its
  pulses from 0 on a jump, so the re-maker's count since Start fell back and it sent nothing until the count caught up (the pulses
  keep their places now); the line kept the tempo the source had left for the six outliers a jump or ramp needs, and its ramp refit
  kept 24 pulses of the old tempo beside the six new ones, which held it behind and widened its spread, and so the threshold, until
  pulses 20 ms off counted as on the line (the refit is through the run alone now); and the echo test's slot rule took a pulse moved
  half an interval early for an extra. Now the follower says when the source is moving (two outliers in a row on one side, until two
  pulses come back on the line), and while it moves the re-maker follows the source's own pulses, each sent when it came and none
  ahead of it; after a single late pulse the next is waited for, at no cost when it comes on time; a single early one is still sent
  on the line, so a DAW's spike is smoothed. Results: steady unchanged; a jump to 140 BPM p99 0.79 ms, to 90 BPM 0.83 ms (at most
  5.9); a tempo turned within a beat p99 1.8 and 4.1 ms; 5% up and back 5.0 ms; the count kept everywhere, the echo test on or off
  the same. Each pulse is held within half an interval of the source's of its place, the bound at which it still stands for it; a
  quarter interval is missed by single pulses where the source speeds up (at most 6.8 ms going to 100 BPM within a beat, 5.4 ms in the
  5% case), before a second early pulse confirms it. Left: a slow ramp still lags by the line's own fit, 2.7 to 3.8 ms over eight
  bars (a relay 0.3). On the synth [V-exp: the same desktop and ports, the tempo turned by hand from 120 to 142, down to 75, its
  display watched]: the count kept in four spans, the phase from the second beat a median of 1.45 to 1.6 ms and p99 2.3 to 2.5 ms
  through the turns (13.7 ms before), one pulse at 7.7 ms past a quarter interval; most of the median is the system's own delivery
  of the input (a median of 1.64 ms after its stamp). The synth's tempo, beat by beat, moved only when turned; the 5% wobble of the
  earlier recording was most likely the knob touched. Phase is still against a software port's writes, not a real receiver's.

## A slow ramp (2026-11-15)

After the tempo changes above, a slow ramp still lagged by the line's own fit: 2.7 to 3.8 ms over eight bars, where a relay keeps
0.3. A review proposed the cheapest change first, before a curvature term: a sustained drift of the residuals to one side counted as
moving, so a slow ramp takes the same path as a fast turn. Built (`Follower.drifted`): over the last beat (`DriftPulses`, 24) of
pulses on the line, a mean residual past 4% of the interval (`DriftOn`), or the line's own spread if larger, is moving, until it falls
under half that. Compared the same way (`TestATempoChangeIsFollowedAndComparedWithARelay`, ten seeds each, 0.3 ms of jitter, on the
system's millisecond stamps) [V-exp, the test]:

| Case | Before: phase median / p99 / at most, ms; past a quarter interval | After |
|---|---|---|
| Steady at 120 BPM | 0.06 / 0.24 / 0.36; none | the same |
| 120 to 140 BPM over eight bars | 2.66 / 3.45 / 4.23; none | 0.31 / 1.19 / 2.01; none |
| 120 to 100 BPM over eight bars | 3.84 / 4.82 / 4.99; none | 0.29 / 1.22 / 2.05; none |
| 120 to 130 BPM within a beat | 0.09 / 1.82 / 3.42; none | the same |
| 120 to 100 BPM within a beat | 0.13 / 4.11 / 6.78; 25 pulses | 0.14 / 2.17 / 5.95; 3 pulses |
| 5% up and back over two beats | 0.09 / 5.01 / 5.41; 52 pulses | 0.09 / 2.22 / 4.91; none |
| A jump to 140 BPM | 0.07 / 0.79 / 3.11; none | the same |
| A jump to 90 BPM | 0.06 / 0.83 / 5.88; 20 pulses | the same |

The price is a slow ramp's spacing: 0.55 ms RMS against the source's true spacing, near a relay's 0.59, where the line kept 0.2. A
listener hears the phase; the spacing during a ramp is the source's own. A steady source is unchanged, jittered included (the jittered
and spiky cases of `remake_test.go` pass), since a beat's mean of its residuals stays near zero. A curvature term, which
could keep both, was not built: the drift met the criteria first. On the recorded synth (`TestADrumMachinesRecordedStartsAreEachFollowedFromTheirFirstPulse`),
the worst pulse of its one wobble fell from 7.1 to 6.7 ms.

A count report fixed beside it: a Stop's owed pulses are written after the transport has stopped, and the report counted a pulse as
written only while it ran, so under load a run of 96 pulses reported 95 (`TestTheHubFollowsAPortsClockAndSendsItOnWithItsCountKept`,
in the full suite); whether a pulse counts is decided now when it is queued.

On the synth after it [V-exp: the same desktop and ports, 2026-11-15, the tempo turned slowly by hand from 120 down to about 25 BPM
over about 70 beats, its display watched]: the count kept (2,271 heard, 2,271 written), and

| Part of the run | Phase median / p99 / at most, ms | Past a quarter interval |
|---|---|---|
| Steady at 120 BPM | 1.46 / 2.29 / 4.43 | none of 360 |
| The slow ramp, 120 down to 46 BPM over about 52 beats | 1.46 / 2.18 / 4.34; spacing sent 0.58 ms RMS against the source's | none of 1,248 |
| Under 46 BPM | 1.83 / 33.4 / 115.8 | 90 of 639 |

The ramp is followed as tightly as steady playing; the hand-turned run before the drift rule had a 99th percentile of 13.7 ms. Under
about 30 BPM the synth's own clock is not even: at a displayed 24.8 BPM (an interval of 100.8 ms) it sent its pulses 83, 84, 83, 83 and
167 ms apart, over and over, the tempo right on average; the system stamped them so, its delivery a median of 1.7 ms, so the pattern is
the device's [V-exp]. Against it the follower neither relays the pattern nor evens it out: its pulses went out 94, 83, 99, 98 and 125
ms apart, up to 33 ms off the synth's, the line's own spread 30 ms. A source that uneven was not among the cases, and what a follower
should send for it (the device's pattern, or an even clock at its average) is open. The one pulse 115.8 ms late, and two others past
half an interval, came with a write the GS Wavetable Synth's driver held 83.6 ms (the time thread's longest write; the gaps sent 181 then
42 ms): the software port's, not the follower's.

## Under load, case F14 (2026-11-15)

The synth as master at 120 BPM, followed for 110 s, with every logical core of the desktop (16) held busy at normal priority by as many
spinning processes from 30 s to about 82 s [V-exp: the same desktop and ports; the load a script of the session's, not in the
repository]. A first run had no Start (the transport not pressed), so its machine only relayed the clock while stopped; a second had three
Starts. Both read the same:

| Window | Delivery after the system's stamp, median / p99 / at most, ms | Relay written after the callback took the pulse (first run) | Counted pulses (second run): phase median / p99 / at most, ms; past a quarter interval; past half | Spacing sent against the source's, RMS |
|---|---|---|---|---|
| Before the load | 1.62 / 2.18 / 4.16 | 0.34 / 0.85 / 4.58 | 1.48 / 2.23 / 2.84; 0 of 612; 0 | 0.54 ms |
| Every core busy | 1.63 / 2.30 / 5.84 | 0.33 / 0.74 / 2.08 | 1.46 / 2.59 / 6.46; 5 of 2,304; 0 | 0.75 ms |
| After it | 1.64 / 2.38 / 4.55 | 0.32 / 0.79 / 2.06 | 1.46 / 2.26 / 6.97; 0 of 1,247; 0 | 0.67 ms |

The count was kept in all three spans (97, 190 and 4,332 pulses heard and written); the source was never taken for lost, held or
stopped; the time thread's longest write was 5.7 ms. The system delivered the synth's input as fast with every core busy as idle, and
the time thread (MMCSS "Pro Audio", time-critical) wrote each pulse as fast: the load cost a third of a millisecond at the 99th
percentile and five pulses in 2,304 past a quarter interval, none past half. This answers, for load from other processes, the question
the stand-in left (a delivery held past the hold stopping the followers): there, the source was a goroutine of the hub's own process,
starved with it; a device's input was not starved. Not covered: load inside the hub's process (its collector, with a program's real
work), memory pressure, a disk's load, and load at a priority above normal.

### F14 again, under ADR-0007 (2026-11-16)

The same case on the current design: every port its writer from the start, timed writes handed ahead, the process above normal
([ADR-0007](../adrs/0007-writes-handed-ahead.md)); the synth as master at 120 BPM for 110 s, a Stop and a Start
in the first 25 s, every logical core (16) busy from 30 to 81 s [V-exp: the same desktop and ports, after a restart of the machine].

| Window | Delivery after the system's stamp, median / p99 / at most, ms | Phase median / p99 / at most, ms; past a quarter interval; past half | Spacing RMS, the source / sent |
|---|---|---|---|
| Before the load (to 28 s) | 1.05 / 1.57 / 1.73 | 1.49 / 2.12 / 2.66; 0 of 1,084; 0 | 0.81 / 0.67 ms |
| Every core busy (32 to 80 s) | 1.02 / 1.56 / 2.64 | 1.43 / 2.93 / 9.36; 9 of 2,304; 0 | 0.58 / 0.75 ms |
| After it, to the first held delivery (84 to 105 s) | 1.01 / 1.57 / 4.97 | 1.29 / 1.97 / 17.3; 3 of 1,017; 1 | 1.09 / 0.63 ms |

Under load the followed clock reads as it did before ADR-0006 (1.46 / 2.59 / 6.46, five past a quarter interval): its pulses are handed
at their time, so the handoff's tail is on their path, as ADR-0007 says, and it costs about a third of a millisecond at the 99th
percentile and four more pulses in 2,304 past a quarter interval, none past half. The time thread's longest write was 5.9 ms.

**After the load, a fault.** Four times (at 85, 105, 106.6 and 107.1 s; the load ended at 81 s) the system stamped no pulse of the synth's
for 42 to 58 ms and then delivered the held ones within a few milliseconds of each other, each stamped as it was let go: 58 ms, then
three pulses 1 and 5 ms apart. The hub's own thread was held at the last three as long (no write for 52 to 67 ms), so the machine, not
the port, stopped [Inf: what held it is not known; nothing ran but the run itself]. The first such gap, 2.8 of the line's intervals, was
past the rule that tells a dropout (2.5), and the follower counted two pulses missing; the burst after it then took the next places, and
every pulse after it went out two places ahead of the synth's (41 ms early) until the run ended, 4,072 written for 4,070 heard. Part D's
count broke, and nothing in the report but the phase said so.

Fixed: while locked, a pulse past the gap is taken as the next in the count until the pulse after it says which it was. One on time
(half an interval or more later, or none by then) means pulses were missing, and the gap's pulse is taken again in the line's place;
one sooner is a burst, the system's held delivery, and the count stands (`Follower.settle`; counted as `Held`, and in `midi follow`'s
report beside the pulses missing). The run's input from its second Start is a test source
(`tests/testdata/drum-machine-stalls.txt`, `TestADrumMachinesHeldDeliveriesKeepTheCount`, the hub held with its input): it failed
before the fix (4,072 sent for 4,070) and passes after it, every pulse away from the holds within 1.1 ms of the synth's; a dropout of
three or ten pulses is still counted missing (`TestDropoutsAreBridgedOrHeldAndASourceBackIsTakenAgain`).

## A cable pulled, case F15

2026-11-15, the synth as master at 120 BPM on its own power, its USB cable pulled at the laptop and put back twice in a run, a short pull
and a long one [V-exp: the same desktop and ports].

- **The hold, then Stop, as designed.** The last pulse heard, the line sent on for the hold's eight beats (744 pulses written against
  553 heard), and past it Stop went to the followers.
- **The input never came back.** Windows closed the followed input with the device, and nothing opened it again: the rest of the run
  was silent, through both returns. The hub's look at the ports (`Listen`, every few seconds in the program) would have done worse, closing
  the followed input when the port went and opening a plain listener in its place when it came back. Fixed: the hub knows its followed
  ports, leaves them to their followings, and tells each whether its port is there (`Revive`, which `Listen` calls and `midi follow` calls
  every second); gone, its input is closed, back, opened again with its stamps mapped afresh
  (`TestAFollowedPortUnpluggedAndPluggedBackIsFollowedAgain`). Run again: both pulls held and stopped the followers; the port came back
  after 8 and 12.7 s (Windows bringing the device back, not the count to two or ten), its input was opened again each time and the
  synth heard, and a Start pressed after the second return was followed, 2,000 pulses heard and 2,000 written.
- **Every other port stalled.** Writes to the GS Wavetable Synth were held up to 960 ms at the first pull, and in the second run, after
  the device came back, 69 writes took over 20 ms, up to 698 ms, so the last span's phase had a 99th percentile of 160 ms. A process of
  its own that only wrote a clock to the GS port every 20.8 ms, and never opened the synth, did the same when the synth was pulled and put
  back: writes of 171, 212, 961 and 443 ms at the pull, 200 to 634 ms while it came back, writes of 10 to 100 ms for about 50 s after,
  then none (233 writes over 10 ms in 100 s). A fresh `midi follow` with nothing pulled had none (its longest write 0.74 ms, with the
  ports looked at every second). So the stall is the system's, not the hub's: a USB MIDI device leaving and coming back holds writes to
  other MIDI ports for close to a minute. Whether a hardware port stalls the same is not measured [Inf: the synth is a USB audio device
  as well, and the software synth renders through the audio engine that rebuilds its endpoints]. On the hub's one time thread a write
  held on one port holds every port's timing: the open question in ADR-0003's action items.

Two more faults found beside it, in generated sources built on the synth's own pattern under 30 BPM (`uneven`, four pulses 83 ms apart
and one 167 ms): a line through such a source fits none of its pulses, and the drift rule could not call it moving, since its threshold
grows with the line's spread; it is moving now when the spread passes a sixth of the interval (`UnevenSpread`), and its own pulses are
sent on (by default a 99th percentile of 2.6 ms, at most 7.0, none past a quarter interval; with the loop guard on, whose test holds
doubtful pulses back, 9.4 and 49.5 ms, inside half an interval). And in one seed of ten a ramp from 120 to 25 BPM within four beats
counted 16 pulses missing that the source never left out: the rule that tells a dropout by a gap of more than 2.5 of the line's
intervals met a line a step behind a slowing source, and the pattern's double gap; it no longer counts while the source moves, drifts or
is uneven, and the count is kept in every seed.

The time to lock is in the report now (`LockedAfter`): the source's pulse on which the follower first locked.

## After idle, case F16

2026-11-15, the synth stopped for 263 s with the computer left untouched (no keyboard or mouse; its screen free to turn off), then
Start, and 105 s of play [V-exp: the same desktop and ports]. The relays while stopped were written 0.31 ms after the callback took each
pulse (median) from the first minute to the last, at most 2.5 to 3.1 ms, and the input's delivery after the system's stamp stayed at a
median of 1.45 to 1.48 ms. After Start the first pulse was written 1.30 ms after the synth's, the first beat's eight first pulses 1.2 to
1.9 ms after theirs (at most 1.93 ms over the beat); the rest kept the count (5,041 heard and written), a median of 1.29 ms and a 99th
percentile of 2.08, one pulse at 13.9 ms with a write the GS port held 12.5 ms. The follower had first locked on the source's 24th pulse,
a beat (`LockedAfter`), inside part G's two beats for a hardware master. What it did not test: the synth sends its clock while stopped, so
the hub relayed a pulse every 21 ms through the idle stretch, keeping USB and the time thread awake; a source silent while stopped (most
DAWs) leaves the port quiet, which is the harder case for the system's power saving, and goes with F17.

## Step 4, what listeners hear (2026-11-16)

Built: `Hub.FollowFrom(port, source, FollowOptions{Hold, Meter, Beats, LoopGuard})`, which the program around the hub offered its
clients for their own MIDI ports. `FollowFrom` stops the port's own clock, takes
it off any source it followed and adds it to the source's following, so one following clocks every port that follows that source;
`Hub.Clock` takes a port back to its own clock, or stops it, and a following left with no port ends, its input listened to again for
listeners. Refused, as part A says: a port following itself, a source made a follower, a port clocked while followed, and a source the
machine clocks (its own clock would come back). The listeners of the followed port hear events of the type `clock` (part E): `start`,
`stop`, `continue`, `position` (a Song Position while stopped, with its bar and beat), `tempo`, `lost`, `back`, `count`, and `bar` or
`beat` when asked, counted under the meter from Start or from a Song Position, the hold's pulses counted, since the followers had them.
The tempo is told when it moves past a band of three standard errors of the line's slope, the wider of the estimate told and the one now,
never under 0.1 BPM: on a steady source with the system's millisecond stamps the first run told it twice (119.94, then 119.99), the
estimate after a beat being the less sure one; with the told estimate's band kept it is told once.

Tested on stand-in ports in real time [V-exp: the suite, `follow_from_test.go`, five runs each]: seven beats in 3/4 told as
seven beats, a steady tempo told once, 120 then 132 BPM told as both, two ports following one source each with every pulse, each refusal,
and a source lost (held a beat, then Stop), a Song Position and Continue (`position`, `continue`, `back`, the bars from the position).

On hardware, the same day [V-exp: the same desktop; the program's MIDI test --follow <synth> --into <drum machine>: the program's server side and
its own binary in the test's process, a real client; the synth as master at 120 BPM, a second device
over USB as the port following it; the owner pressed Play and saw the second device start and play along]: the listener's follow
answered, and it heard Start, the tempo once (120.05), then every bar from 1 to 53, two seconds apart, none missed. The program's
status (each following's state, tempo, pulses heard and written since Start, phase, missing and held) said 5,233
pulses heard and 5,233 written, a phase of 0.63 ms median and 1.23 ms at the 99th percentile, none missing, no held delivery.


Seen on the way: a device left the system's list of MIDI ports during a run that hung, and came back when replugged; that run had a
second program of an earlier run, which had not ended, holding the same two ports. Two programs on one machine's ports is not a case a hub
has (one program a machine), but a device gone while a call holds it is: the run that hung is not explained [Inf: the call opening the
second device's output waited on a device the system was dropping].

## What is left

The rest of the cases (F17: Ableton's baseline, with a source silent while stopped after idle); whether a hardware port stalls when a USB
device leaves and comes back, and a writer per port (ADR-0003); load inside the hub's own process;
the program's `doctor`'s per-port offset (it needs a loop or a device that echoes); the run under load of step 1 (whether the MIDI service itself
stamps late); the phase against a real receiver rather than a software port; a soft-thru loop with the loop guard on, on hardware (the synth re-sends the clock it receives over USB, its clock send, so a loop can be run); and a source whose own clock is uneven (the synth under about 30 BPM): its pattern relayed or an even clock at its average.
