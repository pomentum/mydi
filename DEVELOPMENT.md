# Developing mydi

The rules for working on mydi, for people and for coding agents. [AGENTS.md](AGENTS.md) and [CLAUDE.md](CLAUDE.md) point here and do
not repeat them.

## Layout

```
go.mod, go.sum          the module github.com/pomentum/mydi, every dependency pinned
doc.go                  the package's documentation, as pkg.go.dev shows it
event.go                events as JSON, Encode, Decode, the running-status Parser
port.go                 drivers and ports, IDOf, Virtual ports of files
alsa_linux.go           System on Linux and the Pi: ALSA's raw MIDI device files
winmm_windows.go        System on Windows: Winmm.dll, input stamped by the system
system_other.go         System elsewhere: no port
device_port.go          a USB device's port, found by its identity
devices.go              a MIDI device coming or going, and until when Windows may hold writes for it
hub.go                  the Hub: ports opened when used, Send, Release, Panic, Clock, Listen, Close
scheduler.go            the time thread (ADR-0003): one queue on one timeline, its counts
timeline_*.go, tick_*.go  the timeline and the timer per system (the performance counter and MMCSS on Windows)
writer.go, lander.go    each port's writer (ADR-0006, ADR-0007) and its landing (ADR-0008)
follow.go               the Follower: a device's clock in, tempo and phase out (ADR-0004 part C)
remake.go               the Remaker: the clock sent on, counted (ADR-0004 part D, ADR-0005)
echo.go                 the echo test, a loop guard (ADR-0005 part B)
following.go, tell.go   a followed clock on the hub, and what its listeners hear (ADR-0004 part E)
loopback.go, measure.go the measures: where a system stamps input, the clock's own timing
example_test.go         the examples pkg.go.dev shows, which Go reads only beside the package
tests/                  every test, black-box (package mydi_test); tests/testdata/ holds recorded devices
cmd/mydi/               the measuring tool (ADR-0001 part E)
docs/guides/            how to use and measure it
docs/adrs/              the decisions, numbered from 0000; template.md
docs/trials/            the uses mydi sets out to cover, TST-0001 onwards
docs/reports/           the measurements and research behind the decisions
```

## Commands

Go 1.27.1, the version `go.mod` names. With another Go on the machine, run everything in the pinned image, from the repository's root:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -w /src -e CGO_ENABLED=0 \
  golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66 \
  sh -c 'gofmt -l . && go vet ./... && GOOS=windows go vet ./... && go test ./...'
```

That is what CI runs (`.github/workflows/ci.yml`), with the builds of `cmd/mydi` for Linux, Windows and arm64 and of the Windows test
binary. `MSYS_NO_PATHCONV=1` is for Git Bash on Windows; elsewhere it does nothing.

### The tests that need Windows

WinMM, the performance counter, the waitable timer and MMCSS are Windows's own, and CI has no Windows runner. Build the test binary in
the image and run it on a Windows machine:

```sh
# in the image
GOOS=windows go test -c -o bin/mydi.test.exe ./tests
# then on Windows, from tests/ (testdata/ is read relative to the folder it runs in)
cd tests && ../bin/mydi.test.exe
../bin/mydi.test.exe -test.run TestWindows -test.v
```

### Measures, run only when asked

These depend on the machine and what else runs on it, so they are measures, not checks: they fail on nothing but a clock that does not
run, and no CI runs them.

```sh
# the clock against its grid, 20 s at 120 BPM; _LOAD=n adds n goroutines allocating flat out (or together at _MBPS megabytes a
# second), _SECONDS sets its length, _PORT=<id> plays it on a real port too, _NOTES=n writes n note offs a second beside it
MYDI_TIMING=1 ../bin/mydi.test.exe -test.run TestClockTiming -test.v
MYDI_TIMING=1 MYDI_TIMING_LOAD=4 MYDI_TIMING_MBPS=200 ../bin/mydi.test.exe -test.run TestClockTiming -test.v
# the follower's jittered cases drawn with more seeds than the default twenty
MYDI_FOLLOW_SEEDS=200 go test -run "TestTheFollower|TestARamp|TestAJump|TestDropouts" ./tests
```

`cmd/mydi` measures a machine end to end; [the measuring guide](docs/guides/measuring.md) explains its reports.

## Rules

- **Pure Go.** No cgo; the standard library and `golang.org/x/sys` only. A new dependency is pinned exactly, needs an ADR saying why,
  and adds a row to [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) in the same commit.
- **The API is what a MIDI program needs.** Who may use a port, who owns what is sent and where input goes are the caller's
  (`NewHub`'s arguments); the library decides none of them (ADR-0001 part C). An exported name is documented; a change to one is a
  change of version (`v0.x` while it settles: a minor for a break, a patch otherwise).
- **The time thread never waits on a driver.** It hands each write to the port's writer (ADR-0007); a new timed write goes through it.
  Nothing on its path allocates per pulse or reads the wall clock (ADR-0003 parts A and B).
- **Stalls are counted, not hidden** (ADR-0003 part D): a new way a write can be late adds its count to `Timing`.
- **A note started is a note ended**: anything that drops writes keeps note offs and All Notes Off (ADR-0006 part C).
- **Tests are black-box** (`package mydi_test`), live in `tests/` (the examples alone beside the package), reach the library only through what it exports, and wait for conditions, never fixed
  sleeps, except before a check that something did not happen. A test that needs a device plays a recording (`tests/testdata/`) or a
  virtual port, never the machine's own ports.
- **Decisions first.** A change to how time is kept, how ports are written or how a clock is followed gets an ADR (from
  `docs/adrs/template.md`, the next number) before the code, with its evidence; a measurement goes in `docs/reports/` with its evidence
  label ([ADR-0000](docs/adrs/0000-record-architecture-decisions.md)).
- **Docs with the change**: the README and the guides when the API or a command changes; this file when the layout, commands or rules
  do; `doc.go` for what pkg.go.dev shows.
- **Nothing private.** No names of a machine, its devices' owners, paths of a person's machine, or credentials, in code, tests,
  recordings, reports or commit messages. A recording names its device by kind ("a drum machine"), never by its owner's setup.
- **LF line endings** everywhere (`.gitattributes`).

## Git

- Commit messages are conventional commits, lower case, plain English: `feat(clock): ...`, `fix(follow): ...`, `docs: ...`; types
  `feat`, `fix`, `docs`, `test`, `perf`, `refactor`, `ci`, `chore`.
- Commits are the owner's. No AI co-author line or attribution in a message or trailer.
- Never rebase; merge. Topic branches are `wip/<topic>`, merged into `main`.
- No push, tag or release without the owner's approval. A release is a tag `vX.Y.Z` on `main`; the Go module proxy and pkg.go.dev take
  it from there.
