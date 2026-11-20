# ADR-0001: mydi, a Go MIDI library of its own

| Part | Decision | Status |
|---|---|---|
| A | mydi is a Go module of its own, `github.com/pomentum/mydi`, its code in `src/` forwarded at its root, Apache-2.0, published by semantic version tags | Accepted |
| B | Pure Go: the standard library and `golang.org/x/sys` alone, no cgo; Linux (ALSA raw MIDI) and Windows (WinMM), every other system builds with no ports | Accepted |
| C | Its public API is what a MIDI program needs: events, drivers and ports, the hub, the clock, following a clock, and the measures; what a program decides about its users (who may use a port, what it tells them) stays the program's | Accepted |
| D | Tests at two levels: the library's own, black-box (`mydi_test`), on Linux in CI and on Windows by hand for what needs the system; and each program's own integration tests, which exercise mydi through that program | Accepted |
| E | `cmd/mydi` is the library's measuring tool: the ports listed, the clock measured (as a Windows service too), a loopback, a device's clock followed | Accepted |

**Status:** Accepted, 2026-11-20 (A when `v0.1.0` was published).
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** the package's tests (`go test ./...` on Linux in CI; the Windows tests built as `.test.exe` and run on Windows, see
[DEVELOPMENT.md](../../DEVELOPMENT.md)); the measurements in [`docs/reports/`](../reports/README.md), made with the same code before it
was a module of its own [V-exp].

## Context
The MIDI code began as part of a program that runs other programs' work on a person's machine. It grew to about 6,000 lines that import
nothing of that program: events, ports, a hub with one time thread, a writer per port, and a follower of a device's clock. The owner wants
it reused beyond that program, published as a library.

## Decision
**A.** The module is `github.com/pomentum/mydi` and its package is `mydi`, at the root, so a program imports one path. The code is in
`src/` (package `src`), which the owner asked for (2026-11-20); since Go makes the folder the import path, the root holds one generated
file, `mydi.go`, that forwards every name `src/` exports (types as aliases, constants and variables as the same values, a settings struct
by its address, functions by the same signature), so programs import the root. Its costs, accepted: pkg.go.dev documents each type's
methods on `src`'s page, `/src` can be imported too (the root is the one to use), and the three measuring settings that were package
variables are forwarded by address (`MeasureHold`, `MeasureRestart`) or set through a function (`SetOnHold`). `go generate` writes
`mydi.go` again; a test and CI keep it in step. Versions are
semantic version tags (`v0.x.y` while the API settles); a program pins one exactly. The module is published by making the repository
public and tagging; the Go module proxy and pkg.go.dev then serve it with no other step.

**B.** No cgo, so it cross-compiles from any machine: ALSA's raw MIDI device files on Linux and the Raspberry Pi, `Winmm.dll` through
`golang.org/x/sys/windows` on Windows. On any other system `System` has no ports and everything else works (virtual ports, the follower,
the measures on a stand-in port).

**C.** Exported: `Event`, `Encode`, `Decode`, `Parser`; `Driver`, `Output`, `Input`, `StampedDriver`, `PortInfo`, `System`, `Virtual`,
`IDOf`, `CardID`, `PortOfDevice`; `Hub` (`NewHub`, `Send`, `Release`, `Panic`, `Clock`, `Listen`, `Revive`, `Close`, `Ports`,
`MachinePorts`, `Devices`, `Timing`, `Follow`, `FollowFrom`, `Unfollow`, `Follows`); `Follower`, `Remaker`, `Echo`, `Lander` with their
configurations and defaults; the measures (`MeasureClock`, `MeasureClockBack`, `RunLoopback`, `Listen`, `AnalyseLoopback`) and the
process-wide switches they use (`SetLanding`, `SetLead`, `SetResync`, `SetHoldDrop`, `SetStuckAfter`, `MeasureHold`, `MeasureRestart`,
`OnHold`), which a program leaves alone unless it measures. A program says which ports may be used (`NewHub`'s `allowed`), who owns
what it sends (`Owner`, any string), and where what comes in goes (`listen`); the library never decides those.

**D.** The library's tests live in `tests/` (package `mydi_test`) with the recordings in `tests/testdata/`, and reach it only through
what it exports, as a program does; the examples alone stay beside the package, where pkg.go.dev shows them. Tests that need Windows itself (WinMM, the time
thread's timer and priority) are built as `.test.exe` and run on a Windows machine; the clock's timing on a machine is a measure, run
only when asked (`MYDI_TIMING=1`), never a gate. A program that uses mydi keeps its own integration tests, which exercise mydi through it.

**E.** The measures that made the evidence move with the library as `cmd/mydi`, so anyone can measure their own machine and devices.

## Options considered
| Option | For | Against |
|---|---|---|
| A module of its own (chosen) | Reused by any Go program; versioned and tested on its own | Two repositories to keep in step |
| Stay inside the program | One repository | Not reusable; its tests tied to the program |
| A `midi` package under a subdirectory | Room for more packages | One more path to import for no gain now |

## Consequences
- Easier: a MIDI program in Go with timing measured on real machines; the library's changes reviewed and tested on their own.
- Harder: a change a program needs lands here first, is tagged, then is pinned by the program.
- Revisit when: a second package is wanted (network MIDI, MIDI 2.0), or the API is ready for `v1`.

## Action items
- [x] The repository made public and `v0.1.0` tagged (2026-11-20).
