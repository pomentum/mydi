# Reports

The measurements and research behind mydi's decisions. They were made while mydi was the MIDI part of a larger program, an agent that
ran its users' work on a person's machine, and are kept as they were written, reworded where they named that program: **the program**
is the process around the hub, **a client** one of the program's users sending MIDI through it, and **the program's MIDI test** its
end-to-end test. The commands they name are those of that time; mydi's own measures are `mydi timing`, `mydi loopback` and `mydi follow`
([the measuring guide](../guides/measuring.md)). Each claim carries its evidence label ([ADR-0000](../adrs/0000-record-architecture-decisions.md)).

| Report | What it answers | Decisions |
|---|---|---|
| [A tight MIDI clock from the hub on Windows](midi-clock-on-windows.md) | How the best tools keep time, why Windows makes it hard, what Go adds, and how well the hub's clock keeps time: idle, loaded, as a service, for an hour through a synth | [0003](../adrs/0003-one-time-thread.md), [0006](../adrs/0006-a-writer-per-port.md), [0007](../adrs/0007-writes-handed-ahead.md), [0008](../adrs/0008-a-landing-that-stands-down.md) |
| [Following a device's MIDI clock](following-a-midi-clock.md) | Where the system stamps input, the follower and the re-made clock on generated and recorded sources, then real devices | [0004](../adrs/0004-following-a-devices-clock.md), [0005](../adrs/0005-a-followed-clock-that-moves.md) |
