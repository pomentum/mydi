# The clock through a drum machine, as a library (2026-11-20)

**Question.** Does mydi, as a library of its own and measured with its own tool, clock a hardware device from Windows as well as it did
inside the program it began in? **Date: 2026-11-20.** **Evidence:** [V-exp] on a Windows 11 laptop (16 logical cores, the timer tick held
at 1 ms by other programs), a drum machine over USB, `mydi` built from the commit these lines were committed with, in `golang:1.27.1-bookworm`.

## Method

`mydi timing --port <device> --back <device> --seconds 60`: the hub clocks the drum machine at 120 BPM with Start, and the drum machine
sends its clock back over the same USB port (its clock send), so every pulse is counted and stamped by the system both ways. Run once
idle and once with every logical core busy (16 busy loops at normal priority). The landing at its default, 1 ms; the lead off. Then
`mydi follow --in <device> --out <a software port> --seconds 120` with the drum machine as master.

## Results

| | Idle | Every core busy |
|---|---|---|
| Pulses written / heard back / lost | 2,880 / 2,880 / 0 | 2,880 / 2,880 / 0 |
| A pulse against its grid: median, 99th percentile, worst | 0.30, 0.46, 2.13 ms | 0.15, 0.79, 2.70 ms |
| Pulses more than 1 ms late / more than 5 ms | 10 / 0 | 19 / 0 |
| The longest gap between pulses (20.83 ms each) | 23.3 ms | 23.1 ms |
| The tempo a receiver reads beat by beat, 99% within | 0.22 BPM | 0.29 BPM |
| The longest driver call; writes held past 20 ms | 3.0 ms; 0 | 1.9 ms; 0 |
| The writer's stand-downs ([ADR-0008](../adrs/0008-a-landing-that-stands-down.md)) | 4 | 4 |
| Priority granted | MMCSS "Pro Audio" and time-critical, the process above normal | the same |
| The device's return, median / 99th percentile | 0.22 / 0.70 ms | -0.51 / 0.50 ms |

[ADR-0003](../adrs/0003-one-time-thread.md)'s budget, a 99th percentile under 1 ms, is met idle and loaded, and no pulse was lost
either way. The negative return under load is the measure's, not the device's [Inf]: the time of a write is read when the driver's call
returns, and under load that return comes later than the system's stamp of the same pulse coming back.

**Following.** With the drum machine as master, `mydi follow` sent its clock on to a software port. It locks on the 24th pulse (a beat)
and relays the clock while the device is stopped. A first run heard no transport at all: the device sends its clock and its transport
(Start, Stop, Continue) by two settings, and only the clock's was on. With both on, Play sent Continue (the device resumes where it
stopped) and Stop ended it, a run of 63 s:

| | Result |
|---|---|
| Pulses heard / sent on / compared pulse by pulse | 3,033 / 3,033 / 3,033 |
| Each pulse sent against the source's of its place: median, 99th percentile, worst | 0.62, 1.65, 3.60 ms |
| The spacing's error, RMS: the source's own, the clock sent on | 0.74, 0.42 ms |
| The system's delivery of each pulse after its stamp, median and 99th percentile | 0.99, 1.55 ms |
| The follower's tempo; outliers; losses | 119.999 BPM; 4 in 5,760 pulses; none |

Against [ADR-0005](../adrs/0005-a-followed-clock-that-moves.md) part C: the count kept from Continue to Stop; every pulse within a
quarter interval (5.2 ms at 120 BPM), the worst 3.6 ms; the clock sent on steadier than the source's own. The hardware column's phase,
under 1 ms at the 99th percentile, is missed at 1.65 ms, as in the earlier [record](following-a-midi-clock.md): the phase is measured
against the system's stamps, and the system's own delivery is a millisecond of it [Inf].

## What it does not show

An hour, a dedicated machine, a device on DIN through an interface, a run as a service, a followed clock sent on to a hardware receiver
(the earlier [record](following-a-midi-clock.md) has most of them from inside the program mydi began in), and a source that changes
tempo while followed.
