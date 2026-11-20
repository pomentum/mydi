# Mydi

[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/pomentum/mydi.svg)](https://pkg.go.dev/github.com/pomentum/mydi)

> **mydi** /ˈmi.ði/ (*μύδι*) *noun*. Greek for *mussel*, pronounced like *MIDI*. A lightweight shell for routing and shaping musical data.

mydi plays, clocks and follows MIDI from Go, in pure Go (no cgo):

- **Events as JSON**, one MIDI 1.0 message each, played now or at their time.
- **A clock that keeps its grid**: one time thread owns every timed write, at a priority the system serves on time, and counts every
  pulse it was late.
- **A writer per port**, so a driver that holds one call never holds the other ports.
- **Held notes released** when their owner goes, a port goes away or the hub closes: no stuck notes.
- **A device's clock followed** and sent on to other ports, every pulse counted from Start to Stop, with its tempo and phase.
- **Measures** of a machine's timing and of where its system stamps input, as a library and as the `mydi` command.

| System | Ports | Notes |
|---|---|---|
| Windows 10 and 11 | WinMM (`Winmm.dll`), input stamped by the system | The time thread on the performance counter, a high-resolution waitable timer and MMCSS |
| Linux, Raspberry Pi | ALSA's raw MIDI devices (`/dev/snd/midiC*D*`) | Go's monotonic clock and timers |
| Any other | None (`System` lists no port) | Virtual ports, the follower and the measures work everywhere |

## Install

```sh
go get github.com/pomentum/mydi@latest
go install github.com/pomentum/mydi/cmd/mydi@latest   # the measuring tool
```

## Use

```go
hub := mydi.NewHub(mydi.System{}, func(id string) bool { return true }, func(r mydi.Received) {
	fmt.Println(r.Port, r.Event.Type, r.At)
}, log.Printf)
defer hub.Close()

ports, _ := hub.Ports()                // the ids to play
_ = hub.Clock(ports[0].ID, 120, true)  // a clock at 120 BPM, with Start
note, velocity := 60, 100
_ = hub.Send("song", ports[0].ID, []mydi.Event{
	{Type: "noteOn", Channel: 1, Note: &note, Velocity: &velocity},
	{Type: "noteOff", Channel: 1, Note: &note, At: 500}, // half a second later
})
hub.Listen()                           // what the allowed ports play in, to the function above
hub.Release("song")                    // every note "song" holds ended, its pending events cancelled
```

[Getting started](docs/guides/getting-started.md) goes further: events, owners, the clock, following a device, virtual ports.

## Measure your machine

```sh
mydi                                  # the ports, with their ids
mydi timing --seconds 30 --port <id>  # the clock's timing, on a stand-in port and on a real one
mydi loopback --out <id> --in <id>    # where the system stamps input, through a cable or a soft thru
mydi follow --in <id> --out <id>      # a device's clock followed and sent on
```

[The measuring guide](docs/guides/measuring.md) explains each report; [docs/reports](docs/reports/README.md) holds the measurements
behind the design.

## Documentation

- [Guides](docs/guides/README.md): getting started, measuring, timing on Windows.
- [Decisions](docs/adrs/README.md): why the clock, the writers and the follower are built as they are.
- [Trials](docs/trials/README.md): the uses mydi sets out to cover, and how each is shown.
- [Developing mydi](DEVELOPMENT.md): layout, commands, tests on Linux and Windows, rules.

## License

Apache-2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE). Third-party software: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
