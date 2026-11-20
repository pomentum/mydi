# Getting started

```sh
go get github.com/pomentum/mydi@latest
```

## The hub

A `Hub` holds the ports a program may use. It opens each when first used and closes it when the hub closes.

```go
hub := mydi.NewHub(driver, allowed, listen, logf)
defer hub.Close()
```

- `driver` is where the ports come from: `mydi.System{}` for the machine's own (ALSA on Linux, WinMM on Windows), a
  `&mydi.Virtual{Dir: dir, Names: []string{"synth"}}` for ports that are files, or your own `Driver`.
- `allowed(id)` says whether the program may use a port. A port opened is often a port another program loses, so open only what you
  mean to (`func(string) bool { return true }` allows all).
- `listen(Received)` is called with what an allowed port plays in, once `hub.Listen()` has opened the inputs. Call `Listen` again every
  few seconds to pick up a device plugged in; it also reopens a followed input whose device came back (`Revive` does that alone, for a hub that does not listen).
- `logf` is a `log.Printf`-shaped function for what the hub has to say.

`hub.Ports()` lists the allowed ports with their ids, names and directions; `hub.MachinePorts()` lists them all. An id is the port's name
made safe (`mydi.IDOf`), so it stays the same across runs while the device keeps its name.

## Events

An `Event` is one MIDI 1.0 message as JSON, so a program can take them from anywhere:

```json
{"type": "noteOn", "channel": 1, "note": 60, "velocity": 100}
{"type": "noteOff", "channel": 1, "note": 60, "at": 500}
{"type": "cc", "channel": 1, "controller": 74, "value": 20}
```

Types: `noteOn`, `noteOff`, `polyPressure`, `cc`, `program`, `channelPressure`, `pitchBend`, `start`, `continue`, `stop`, `sysex`.
Channels are 1 to 16. `at` plays the event that many milliseconds after the send; without it, at once. `mydi.Encode` and
`mydi.Decode` turn an event into its bytes and back; `mydi.Parser` reads a byte stream with running status.

## Owners and held notes

`hub.Send(owner, port, events)` sends as an owner, any string: a client, a song, a track. The hub remembers the notes each owner holds and
its pending events. `hub.Release(owner)` cancels its pending events and ends its notes (a note off for each, then All Notes Off on each
channel it used); `hub.Panic(port)` ends every note on a port; `hub.Close` ends everything. Release an owner when it goes away, and no
note is left stuck.

## The clock

```go
hub.Clock("synth", 120, true)   // 24 pulses a quarter note at 120 BPM (20 to 300), with Start
hub.Clock("synth", 96, true)    // a new tempo, on the same grid
hub.Clock("synth", 0, false)    // Stop
```

The clock is made by the hub's time thread on a grid that does not drift ([ADR-0003](../adrs/0003-one-time-thread.md)).
`hub.Timing()` says how well it kept time: pulses late by more than 1 and 5 ms, the longest gap, each port's writer's counts.

## Following a device's clock

```go
err := hub.FollowFrom("synth", "drum-machine", mydi.FollowOptions{Meter: "4/4", Beats: "bar"})
```

The port `synth` stops its own clock and sends on the clock that comes into `drum-machine`, every pulse counted from Start to Stop.
`listen` hears what happens as events of type `clock`: `start`, `stop`, `continue`, `position`, `tempo`, `lost`, `back`, `count`, and
`bar` or `beat` when asked. Set `LoopGuard` where the hub's own clock can come back into the source (a soft thru, a chain closed round).
`hub.Clock(port, ...)` takes the port back to its own clock; `hub.Follows()` reports each following. The follower itself
(`mydi.Follower`) is plain code, usable on any pulse times ([ADR-0004](../adrs/0004-following-a-devices-clock.md)).

## Virtual ports

`mydi.Virtual` ports are files in a folder: what a port plays is appended to `<name>.out.jsonl`, and a line appended to
`<name>.in.jsonl` is played in, as an event or as the output writes it (with its bytes, so a device's clock can be played in and
followed). Use them in tests and to try a program with no device. With `Beside: mydi.System{}`, the machine's ports are listed after the
virtual ones.
