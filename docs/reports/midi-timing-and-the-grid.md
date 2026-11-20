# MIDI timing and the clock's grid

**Question.** How does the hub keep a MIDI clock's grid, what stalls it, and how could it be made reliable, in order of cost? A section of
an earlier report on time and clocks on the machines mydi first ran on (2026-11-13), kept here as it was written and reworded as the
[other reports](README.md) are; the rest of that report was about the program's own wall clock, not MIDI. Its plan was carried out in
[the Windows report](midi-clock-on-windows.md) and [ADR-0003](../adrs/0003-one-time-thread.md).

## The grid is already monotonic; the stalls are the scheduler's

### What the hub does

An event's `at` is milliseconds ahead of the call, 0 to 60,000, and the hub plays it with `time.AfterFunc` from the moment the call
arrives [V-code: `internal/midi/event.go`, `hub.go`]. No wall clock is involved, so an NTP step changes nothing. The clock keeps its grid
from its start with one reused `time.Timer`, so a pulse allocates nothing; it catches up a short stall with pulses never closer than a
quarter interval, and moves the grid on after a stall longer than four pulses [V-code: `hub.go`, `Clock`]. A tempo change takes effect
at the next pulse on the same grid. The working tree's `DEVELOPMENT.md` says the clock runs "on a thread of its own" and that a stalled
pulse moves the grid "rather than sending the missed ones in a burst"; the code locks no thread and catches up stalls of up to four
pulses, so the text is out of date [V-code].

Two gaps follow from the design [Inf, from the code]. First, **there is no shared timeline**. Each call's `at` is relative to its own
arrival, and the clock's grid is relative to its own start, so a listener cannot say "on the next beat" or align two calls; the run's own
jitter between calls goes straight into the music. Second, **inbound delivery is serial**: one goroutine posts each event to each
listening client in turn, with a 2 s timeout, and checks the 250 ms window only before the first post [V-code: the program's MIDI delivery,
then]. A slow client delays every event behind it, and those events are then dropped as late. ALSA's raw MIDI also carries no
timestamps ([V-web] [ALSA RawMidi](https://www.alsa-project.org/alsa-doc/alsa-lib/rawmidi.html), cited in ADR-0002), so an inbound
event's time is when a program's reader woke, not when the byte arrived [Inf].

### What was measured, and what it means

The clock's own test measures pulses against their ideal grid (`MYDI_TIMING=1`, 20 s at 120 BPM). On a Windows 11 desktop with
other work running, on Go 1.27.1, pulses landed about 0.6 ms off the grid at the median, 11 ms at the 99th percentile, with gaps up to
50 ms in one run and 100 ms in another, which moved the grid once. In a Linux container on the same machine (Docker Desktop's VM) the
median was 0.46 ms, the 99th percentile 15 ms, the longest gap 57 ms. An earlier version that locked the clock to an operating-system
thread was worse on Windows (99th percentile gap 89 ms) [V-exp, this session, one machine, not a Pi]. At 120 BPM a pulse comes every
20.8 ms, so a 50 ms gap loses or bunches two pulses and a 100 ms gap almost five [Inf, arithmetic].

The median is already good enough. A vendor's measurement of DAWs' MIDI clock with 4 ns hardware found a median of 990 µs RMS jitter
across 15 DAW and system pairs, 0.8 to 5 ms on Windows and about 40 µs for the best on macOS ([V-web]
[Sim'n Tonic](https://simntonic.com/blogs/blog/how-much-does-u-sync-2-0-reduce-your-daw-s-midi-clock-jitter); one interface, 20 s per
run, no maxima). USB-MIDI's bulk transfers wait for a 1 ms frame, and reported interfaces show 2 to 4 ms of latency and 0.3 to 2 ms of
jitter (seen only in search summaries of
[linux-audio-dev](https://lists.linuxaudio.org/archives/linux-audio-dev/2010-July/027411.html) and related threads). A three-byte
message on a 5-pin DIN cable takes about 1 ms at 31,250 baud [Inf, arithmetic, the MIDI specification not re-read]. **So sub-millisecond
typical jitter costs nothing that the wire does not already cost; the tail of 50 to 100 ms is what a synth following the clock would
hear as a tempo that jumps.** E-RM's report says large cycle-to-cycle jitter can exceed a receiver's PLL lock range (search summary only,
the PDF could not be read: [E-RM](https://www.e-rm.de/data/E-RM_report_Jitter_02_14_EN.pdf)), and no receiver tolerance is published.

On a Pi the tail may be worse. A 2024 workshop paper reports a Pi 5 under full CPU load at about 36.8 ms worst case on the stock kernel
and about 124 µs with `PREEMPT_RT` (search summary only; the PDF could not be decoded:
[OSPERT 2024](https://antonio.paolillo.be/publications/workshops/ecrtsOspert2024_dewit_rtlinux_paper.pdf)). `PREEMPT_RT` is in mainline
since 6.12 and builds for 64-bit Pis without a patch, but Raspberry Pi's documentation does not say Raspberry Pi OS ships an RT kernel
([V-web] [Raspberry Pi: The Linux kernel](https://www.raspberrypi.com/documentation/computers/linux_kernel.html)). A `.deb` for stock
Raspberry Pi OS cannot assume one [Inf].

### How to make it reliable, in order of cost

**Real-time priority for one thread, on Linux.** An unprivileged process may raise a thread to `SCHED_FIFO` up to its `RLIMIT_RTPRIO`
without any capability, and policy is per thread ([V-web] [sched(7)](https://man7.org/linux/man-pages/man7/sched.7.html)). systemd sets
the limit with `LimitRTPRIO=` ([V-web] [systemd.exec](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html)), and
`golang.org/x/sys/unix` has `SchedSetAttr` and `Gettid` as pure Go system calls ([V-web] [x/sys/unix](https://pkg.go.dev/golang.org/x/sys/unix)).
The kernel keeps 5% of each second for other work by default (`sched_rt_runtime_us` 950,000), `RLIMIT_RTTIME` caps a runaway, and
`SCHED_RESET_ON_FORK`, "intended for media-playback applications", keeps children from inheriting the priority (all [V-web]
[sched(7)](https://man7.org/linux/man-pages/man7/sched.7.html)). So a program's unit gets `LimitRTPRIO=` and no `CPUSchedulingPolicy=`
(which would make the garbage collector's threads real-time too), and the clock goroutine, locked to its thread, raises that thread
alone with reset-on-fork, so the clients it starts never inherit it [Inf]. One catch: a Go timer is fired by the runtime on another
thread of normal priority, which then wakes the locked goroutine, so the priority partly goes unused [Inf]. The thread should instead
sleep in the kernel itself, with `clock_nanosleep` on `CLOCK_MONOTONIC` at an absolute time [Inf, to verify against the runtime]. The
Windows result with a locked thread does not predict this: that version raised no priority. A project on a Pi 5 with Ubuntu reports RT
priorities refused under cgroup v2 on a kernel built with `RT_GROUP_SCHED` (search summary only:
[ShekharShwetank/RTOS](https://github.com/ShekharShwetank/RTOS)); whether Raspberry Pi OS's kernel sets it was not found.

**Little garbage on the hot path.** Go's collector takes 25% of CPU in its mark phase and makes allocating goroutines assist it; its
pauses are brief and not proportional to the heap ([V-web] [Go GC guide](https://go.dev/doc/gc-guide)). The clock already allocates
nothing per pulse [V-code]; scheduled events allocate a timer and a closure each [V-code: `Send`], which is fine at musical rates [Inf].

**ALSA's sequencer, for the tail.** The sequencer holds timestamped events in kernel queues, in real time or in ticks at a PPQ and tempo,
and dispatches them "at the right time"; a tempo change is one control event ([V-web]
[alsa-lib: Sequencer](https://www.alsa-project.org/alsa-doc/alsa-lib/seq.html)). Written 50 to 200 ms ahead, events and clock pulses
would no longer depend on the program being scheduled on time, and stalls shorter than the lookahead would vanish [Inf]. It also gives a
shared timeline, and lets other programs share a port. The cost is real: no pure-Go sequencer client was found, only PCM and control
libraries (search summaries only, for example [yobert/alsa](https://pkg.go.dev/github.com/yobert/alsa)), so the program would write
`/dev/snd/seq`'s ioctls from `asequencer.h` itself, and a tempo change that must act at once would remove events already queued [Inf].

**Windows.** Go 1.23 brought high-resolution waitable timers, about 0.5 ms instead of 15.6 ms ([V-web]
[Microsoft for Go Developers](https://devblogs.microsoft.com/go/high-resolution-timers-windows/)). The measured stalls are therefore the
operating system's, not Go's timers' [Inf]. Thread priority or MMCSS's "Pro Audio" class are the Windows counterparts of `SCHED_FIFO`,
callable without CGo; no page on them was read.

**Kernel options, for an operator.** An RT kernel or a core reserved with `isolcpus=` would tighten the tail further, but they change the
machine's boot and belong in a guide, not the installer [Inf].

