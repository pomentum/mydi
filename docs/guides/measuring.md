# Measuring a machine

`mydi` measures how well a machine keeps MIDI time, with the same code a program using the library runs. Install it with
`go install github.com/pomentum/mydi/cmd/mydi@latest`, or build it (`go build -o bin/mydi ./cmd/mydi`). Each command opens the ports it
names: close any other program that holds them first.

## The ports

```sh
mydi          # each port's id, its directions (in, out) and its name; opens none
```

## The clock: `mydi timing`

The hub's clock run on a stand-in port that stamps each pulse as it is written, and on a real port too with `--port`. It prints a JSON
report: the pulses, how far each was off its grid (median, 99th percentile, largest), the spacing's error, the tempo a receiver would read
beat by beat, the time thread's counts and each writer's (landed, landed late, woken late, stand-downs), and the system's timer tick at
the start and the end.

| Flag | What |
|---|---|
| `--bpm 120`, `--seconds 30` | The tempo and the length |
| `--port <id>` | A real port clocked as well, its own cost counted |
| `--back <id>` | Where a device sends the clock back (a soft thru, or a synth's clock send): every pulse counted and stamped as the system had it |
| `--out <file>` | The report written to a file as well |
| `--service <name>` | Windows: run as the service of that name, measure once and stop, to measure the priority and class a service gets |
| `--landing <ms>` | The writers' landing (1 ms by default, [ADR-0008](../adrs/0008-a-landing-that-stands-down.md)); 0 turns it off |
| `--lead` | The time thread's timer aimed early by its own wake-up estimate, for an A/B ([ADR-0003](../adrs/0003-one-time-thread.md) part C) |
| `--hold-after <n>`, `--hold-ms <ms>`, `--hold-every <n>` | A pulse's write held on purpose, to see what a device makes of the catch-up or the resync |
| `--resync-after-ms`, `--resync-pause-ms`, `--hold-drop` | The writer's rule after a hold, changed for the run ([ADR-0006](../adrs/0006-a-writer-per-port.md) part C) |
| `--hold-flag <file>` | A file written the moment a write is held past 100 ms, to stop a system trace just after one |
| `--restart-at <s>`, `--restart-pause <s>` | The clock stopped and started again in the same process, to tell a clock's Start from a process's |

Read it: on an idle desktop the 99th percentile off the grid is under a millisecond and no pulse is 5 ms late; a pulse late by tens of
milliseconds on Windows is almost always a write the system held ([the Windows guide](windows.md)).

## Where the system stamps input: `mydi loopback`

```sh
mydi loopback --out <id> --in <id>   # the hub's pulses out and back in, through a cable or a soft thru
mydi loopback --in <id>              # a device's own clock, listened to
```

Its pulses are read back with the system's stamp and the stamp the hub's callback takes, while the read is held on purpose
(`--hold-every`, `--hold-ms`). If the system's stamps keep the pulses' spacing however late they are read, the system stamps on arrival
and a follower can fit on them ([ADR-0004](../adrs/0004-following-a-devices-clock.md) part B); if they bunch with the reads, it stamps
on delivery. `--write-hold-every`, `--write-hold-ms` and `--catch-up-ms` hold and catch up the writes as a port's writer does; `--json`
prints every pulse.

## A device's clock: `mydi follow`

```sh
mydi follow --in <id> --out <id>[,<id>] --seconds 60
```

The clock that comes into `--in` followed and sent on, re-made, to each `--out`. Every second it prints the follower's state, the
tempo, the pulses heard and sent since Start, the phase against the source and any pulse missing. `--loop-guard` turns the echo test on
where the hub's own clock can come back into `--in`; `--record <file>` writes every clock and transport message heard and written, which
a test can replay (as `tests/testdata/` does); `--json` prints the final report.

The measurements behind the library's decisions, and how they were made, are in [docs/reports](../reports/README.md).
