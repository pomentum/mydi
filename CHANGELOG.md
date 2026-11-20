# Changelog

Every release of mydi, newest first. Versions follow [semantic versioning](https://semver.org/); while mydi is at `v0`, a breaking change
takes a new minor version and says so here.

## v0.1.0 (2026-11-20)

The first release.

- Events as JSON, one MIDI 1.0 message each, with `Encode`, `Decode` and a running-status `Parser`.
- The machine's ports: ALSA's raw MIDI on Linux, WinMM on Windows, no cgo; virtual ports of files for tests and tryouts; a USB device's
  port found by its identity.
- The `Hub`: ports opened when used, events played now or at their time, held notes released per owner, input handed on with its time.
- One time thread for every timed write, on the system's own clock and timer at a priority Windows serves on time, its stalls counted
  (`Hub.Timing`).
- A writer per port, so a driver that holds a write holds only its port; each timed write handed ahead and landed on its time, standing
  down under load.
- A device's clock followed and sent on (`Hub.FollowFrom`, `Hub.Follow`), every pulse counted from Start to Stop, its transport, tempo,
  bars and beats told to listeners; an echo test for a clock that comes back.
- Measures of a machine's timing and of where its system stamps input, as functions and as the `mydi` command (`timing`, `loopback`,
  `follow`).
