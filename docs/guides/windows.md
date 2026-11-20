# Timing on Windows

Every program's MIDI on Windows 11 goes through Windows MIDI Services, and the system decides when a thread wakes. mydi does what a
process can ([ADR-0003](../adrs/0003-one-time-thread.md)): one time thread on the performance counter and a high-resolution waitable
timer, in MMCSS's "Pro Audio" task at critical priority, the process above normal and not throttled, and a writer per port so a held
write holds only its port ([ADR-0006](../adrs/0006-a-writer-per-port.md), [ADR-0007](../adrs/0007-writes-handed-ahead.md)). What it
cannot change is the machine's, and these are the rules the measurements gave ([the Windows report](../reports/midi-clock-on-windows.md)):

- **Plug every MIDI device in before the show and unplug none during it.** While a device comes or goes, Windows MIDI Services holds
  other ports' writes: a device unplugged held the clock's writes up to 215 ms for about 30 s. The hub keeps the count through it
  (`Hub.Devices` says until when writes may be held), but a receiver hears the gap.
- **Set a synth's USB to MIDI alone when mydi clocks it**; where its USB audio is wanted too, clock it on its DIN MIDI through an
  interface. With its USB audio on, Windows held writes past 150 ms a few times an hour, and a synth that runs on its own clock in a gap
  stays off the grid until its next Start; with MIDI alone an hour held one write, 117 ms.
- **The timer tick.** Windows' default tick is 15.625 ms; programs often hold it at 1 ms. mydi's timer does not depend on it, but each
  report of `mydi timing` names the tick at its start and end, so a measure says which it ran under.
- **A dedicated machine is quieter than a desktop.** Measure on the machine that will play (`mydi timing --port <id> --seconds 3600`),
  idle and loaded, as a service if the program runs as one (`--service`).
