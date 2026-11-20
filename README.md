# Mydi

[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/pomentum/mydi.svg)](https://pkg.go.dev/github.com/pomentum/mydi)
[![Website](https://img.shields.io/badge/website-www.pomentum.dev-1D5BD6)](https://www.pomentum.dev/)

> **mydi** /ˈmi.ði/ (*μύδι*) *noun*. Greek for *mussel*, pronounced like *MIDI*. A lightweight shell for routing and shaping musical data.

mydi plays, clocks and follows MIDI from Go, in pure Go (no cgo):

- **Events as JSON**, one MIDI 1.0 message each, played now or at their time.
- **A clock that keeps its grid**: one time thread owns every timed write, at a priority the system serves on time, and counts every
  pulse it was late.
- **A writer per port**, so a driver that holds one call never holds the other ports.
- **Held notes released** when their owner goes, a port goes away or the hub closes: no stuck notes.
- **A device's clock followed** and sent on to other ports, every pulse counted from Start to Stop, with its tempo and phase.
- **Measures** of a machine's timing and of where its system stamps input, as a library and as the `mydi` command.

| System | Ports | Timing | State |
|---|---|---|---|
| Windows | WinMM (`Winmm.dll`), input stamped by the system | The performance counter, a high-resolution waitable timer and MMCSS | Measured on Windows 11 ([reports](docs/reports/README.md)) |
| Linux | ALSA's raw MIDI devices (`/dev/snd/midiC*D*`) | Go's monotonic clock and timers | Its tests run on Linux in CI; built for `amd64` and `arm64` (a Raspberry Pi); not yet measured with a device |
| Any other | None (`System` lists no port) | Go's | Virtual ports, the follower and the measures work everywhere |

mydi is at `v0`: its API may still change, a breaking change taking a new minor version. It needs Go 1.27.1 or later
and nothing else: the standard library and `golang.org/x/sys`.

## Install

```sh
go get github.com/pomentum/mydi@latest
go install github.com/pomentum/mydi/cmd/mydi@latest   # the measuring tool
```

## Use

A whole program: a clock and a note on the machine's first MIDI output, and every input printed.

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/pomentum/mydi"
)

func main() {
	// every port of the machine may be used; what the inputs play in is printed
	hub := mydi.NewHub(mydi.System{}, func(string) bool { return true }, func(r mydi.Received) {
		fmt.Println(r.Port, r.Event.Type, r.At)
	}, log.Printf)
	defer hub.Close()

	ports, err := hub.Ports()
	if err != nil {
		log.Fatal(err)
	}
	synth := ""
	for _, p := range ports {
		if p.Out {
			synth = p.ID
			break
		}
	}
	if synth == "" {
		log.Fatal("no MIDI output on this machine")
	}

	hub.Listen()                    // the inputs opened, their messages to the function above
	_ = hub.Clock(synth, 120, true) // a clock at 120 BPM, with Start
	note, velocity := 60, 100
	_ = hub.Send("song", synth, []mydi.Event{
		{Type: "noteOn", Channel: 1, Note: &note, Velocity: &velocity},
		{Type: "noteOff", Channel: 1, Note: &note, At: 500}, // half a second later
	})
	time.Sleep(2 * time.Second)
	_ = hub.Clock(synth, 0, false) // Stop
	hub.Release("song")            // any note "song" still holds ended, its pending events cancelled
}
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

- [Guides](docs/guides/README.md): getting started, trying it with a synth on Windows, measuring, timing on Windows.
- [Decisions](docs/adrs/README.md): why the clock, the writers and the follower are built as they are.
- [Trials](docs/trials/README.md): the uses mydi sets out to cover, and how each is shown.
- [Developing mydi](DEVELOPMENT.md): layout, commands, tests on Linux and Windows, rules.
- [Changelog](CHANGELOG.md): what each release changed.

## License

[Apache License 2.0](LICENSE).
