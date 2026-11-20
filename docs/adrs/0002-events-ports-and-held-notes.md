# ADR-0002: Events, ports and held notes

| Part | Decision | Status |
|---|---|---|
| A | Who may use which port and how a program's users reach it are the program's (the hub takes `allowed` and owners as given) | Not this library's |
| B | Events as JSON, one MIDI 1.0 message each; a sender says what and when (`at`, milliseconds from the send), the hub plays it at that time; the clock is made by the hub; no sound is made | Accepted |
| C | Held notes are tracked per owner, port and channel, and released whenever their owner is released, a port goes away or the hub closes: a note off for each, then All Notes Off on each channel used | Accepted |
| D | Running without the network is the program's concern; the hub never needs one | Not this library's |
| E | Ports: ALSA's raw MIDI on Linux and the Pi, Windows' multimedia MIDI (WinMM), and a virtual port of files for tests; network MIDI later | Accepted |
| F | A USB device names its port by its identity: the raw device below it on Linux, the one port holding its name on Windows (`PortOfDevice`), so a replug keeps it | Accepted |

**Status:** B, C, E and F Accepted, 2026-11-20; A and D decide nothing here.
**Date:** 2026-11-20
**Deciders:** the owner
**Evidence:** `midi_test.go` (events encoded and parsed with running status, timed sends, an owner's notes released and another's left
alone, a port that fails), `virtual_test.go`, `ports_test.go`, `device_port_test.go`, `winmm_windows_test.go` (on Windows); a synth
played and clocked from both systems [V-exp, [the Windows report](../reports/midi-clock-on-windows.md)].

## Context
A program that plays MIDI needs a format its own users can write (often from another language, as JSON), a way to say when, and a
guarantee that a note it started is ended when it goes away: a stuck note is the failure a musician notices first.

## Decision
**B.** An `Event` is one MIDI 1.0 message: `noteOn`, `noteOff`, `polyPressure`, `cc`, `program`, `channelPressure`, `pitchBend`, the
transport (`start`, `continue`, `stop`) and `sysex`; channels 1 to 16 as players count them; `at` delays it by milliseconds. The
clock is the hub's own (`Hub.Clock`), never a stream of events a sender times. Input is parsed with running status and handed on with
its time; clock and active sensing are not handed on as events, dozens a second for nothing.

**C.** `Hub.Send` takes an owner, any string, and the hub remembers the notes it holds and the events still pending. `Hub.Release(owner)`
cancels its pending events and releases its notes; `Hub.Panic(port)` releases every note on a port; `Hub.Close` releases everything.

**E.** `System` is the machine's MIDI: on Linux the files `/dev/snd/midiC*D*`, on Windows `midiOut*` and `midiIn*` of `Winmm.dll`,
with input stamped by the system (`StampedDriver`). `Virtual` ports are files: what a port plays is written to `<name>.out.jsonl` as an
event and its bytes, and a line appended to `<name>.in.jsonl` is played in.

## Consequences
- Easier: a program in any language drives MIDI by JSON through its own front; no stuck notes when it goes.
- Harder: timing is only as good as the time thread ([ADR-0003](0003-one-time-thread.md)) and each port's writer
  ([ADR-0006](0006-a-writer-per-port.md)).
- Revisit when: network MIDI or MIDI 2.0's UMP is wanted.
