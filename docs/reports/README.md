# Reports

The measurements and research behind mydi's decisions. They were made while mydi was the MIDI part of a larger program, an agent that
ran its users' work on a person's machine, and are kept as they were written, reworded where they named that program: **the program**
is the process around the hub, **a client** one of the program's users sending MIDI through it, and **the program's MIDI test** its
end-to-end test. The commands they name are those of that time; mydi's own measures are `mydi timing`, `mydi loopback` and `mydi follow`
([the measuring guide](../guides/measuring.md)). Each claim carries its evidence label ([ADR-0000](../adrs/0000-record-architecture-decisions.md)).

The decision records carry their own measurements too: each ADR from [0003](../adrs/0003-one-time-thread.md) to
[0008](../adrs/0008-a-landing-that-stands-down.md) keeps the results, tables and runs that decided it, from every point of view measured:
at the hub (each pulse against its grid), at the device (pulses sent back by a synth, its tempo display), through a loopback, as a
service, under load, and for an hour.

| Report | What it answers | Decisions |
|---|---|---|
| [A tight MIDI clock from the hub on Windows](midi-clock-on-windows.md) | How the best tools keep time, why Windows makes it hard, what Go adds, and how well the hub's clock keeps time: idle, loaded, as a service, for an hour through a synth | [0003](../adrs/0003-one-time-thread.md), [0006](../adrs/0006-a-writer-per-port.md), [0007](../adrs/0007-writes-handed-ahead.md), [0008](../adrs/0008-a-landing-that-stands-down.md) |
| [Following a device's MIDI clock](following-a-midi-clock.md) | Where the system stamps input, the follower and the re-made clock on generated and recorded sources, then real devices | [0004](../adrs/0004-following-a-devices-clock.md), [0005](../adrs/0005-a-followed-clock-that-moves.md) |
| [MIDI timing and the clock's grid](midi-timing-and-the-grid.md) | How the hub keeps its grid, what stalls it, and the plan in order of cost, written before the Windows measures | [0003](../adrs/0003-one-time-thread.md) |
| [The clock spike](clock-spike/README.md) | The throwaway Windows program that compared timing strategies first (Go's timers, waitable timers, MMCSS, priorities), with its results; a module of its own, run as its README says | [0003](../adrs/0003-one-time-thread.md) |
| [The clock through a drum machine, as a library](clock-through-a-drum-machine.md) | mydi on its own, measured with `mydi timing --back` through a drum machine over USB, idle and loaded, and following its clock (2026-11-20) | [0003](../adrs/0003-one-time-thread.md), [0008](../adrs/0008-a-landing-that-stands-down.md) |
