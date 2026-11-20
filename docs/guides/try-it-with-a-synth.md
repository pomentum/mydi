# Try it with a synth on Windows

What you need: a Windows 11 machine, a synth (or a drum machine) on USB, or on 5-pin DIN through a USB MIDI interface, and
[Go 1.27.1 or later](https://go.dev/dl/). Close any program that holds the synth's MIDI ports (a DAW, the synth's editor): a port one
program opens is often a port another loses. The commands below are for PowerShell.

## 1. Set the synth up

- Turn its **MIDI clock receive** on (often under Sync, Clock or MIDI settings), on the USB port or the DIN input you use.
- If it can send its USB as **MIDI only** (no USB audio), choose that: with USB audio on, Windows holds MIDI writes for over 150 ms a
  few times an hour ([the Windows guide](windows.md)).
- Plug every device in now; unplugging one during a run holds the other ports' writes for up to half a minute.

## 2. Get the measuring tool and find the ports

```powershell
go install github.com/pomentum/mydi/cmd/mydi@latest
mydi
```

`mydi` lists each port with its id, its directions and its name, and opens none. Note the synth's output id (`out`) and, if it sends
anything back, its input id (`in`). From a clone of the repository, `go build -o bin\mydi.exe .\cmd\mydi` gives the same tool.

## 3. Clock it

```powershell
mydi timing --port <synth-out> --seconds 60
```

The synth should show 120 BPM and run in time; its display is the device's own view of the clock. The JSON report is the hub's view:
the pulses, how far each was off its grid (median, 99th percentile, largest), the tempo a receiver reads beat by beat, and the
writer's counts. On a quiet machine the 99th percentile is under a millisecond.

If the synth sends the clock back (its clock send, or a soft thru), measure what it received too:

```powershell
mydi timing --port <synth-out> --back <synth-in> --seconds 60
```

Every pulse that comes back is counted and stamped by the system, so a lost pulse shows in `lost` (`written` against `heard`). Run it
again with a load on the machine to see what the writers do under it ([the measuring guide](measuring.md) explains every field).

## 4. Play notes from your own program

```powershell
mkdir hello; cd hello
go mod init hello
go get github.com/pomentum/mydi@latest
```

Save [the README's program](../../README.md#use) as `main.go`, then `go run .`: it clocks the first MIDI output at 120 BPM for two
seconds and plays middle C for half a second. To pick the synth by name, compare `p.Name` in its loop.

## 5. Follow the synth's clock

Make the synth the master: its **clock send** on, and its transport running from its own Play. Then send its clock on to another device:

```powershell
mydi follow --in <synth-in> --out <other-out> --seconds 120
```

Press Play on the synth. Every second the tool prints the follower's state, the tempo, the pulses heard and sent since Start, and the
phase against the synth; the other device should start and play along. Add `--loop-guard` if the hub's own clock can come back into the
synth's input (a soft thru), and `--record run.txt` to keep every clock message heard and sent.

## 6. Run the library's Windows tests

From a clone of the repository:

```powershell
go test -c -o bin\mydi.test.exe .\tests
cd tests
..\bin\mydi.test.exe
```

They use virtual ports and recordings, never your synth. To measure the clock on the synth's port with the test's own measure:

```powershell
$env:MYDI_TIMING = "1"; $env:MYDI_TIMING_PORT = "<synth-out>"
..\bin\mydi.test.exe -test.run TestClockTiming -test.v
```

## What to send back

If something is off, the JSON of `mydi timing` (with `--back` when the synth sends the clock back) and a line on the machine (Windows
version, busy or idle, USB audio on or off) tell most of the story. A `--record` file of `mydi follow` can be replayed as a test.
