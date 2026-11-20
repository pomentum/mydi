// Package mydi plays, clocks and follows MIDI from Go, on Linux (ALSA's raw MIDI devices) and Windows (WinMM), with no cgo.
//
// Its pieces:
//
//   - [Event]: one MIDI 1.0 message as JSON, with [Encode], [Decode] and a running-status [Parser].
//   - [Driver]: the machine's ports ([System]), and [Virtual] ports of files for tests and tryouts.
//   - [Hub]: the ports a program may use, opened when first used; events played now or at their time by one time thread; the
//     clock, on a grid that does not drift; the notes each owner holds, released when it goes; what comes in, handed on with its time.
//   - [Hub.FollowFrom] and [Hub.Follow]: a device's clock followed and sent on, re-made from the follower's estimate ([Follower],
//     [Remaker]), with an echo test ([Echo]) for a clock that comes back.
//   - Measures: [MeasureClock], [MeasureClockBack], [RunLoopback] and [Listen], how well a machine keeps time and where it stamps input.
//
// Each port's driver calls are made by a writer of its own, so a driver that holds one call never holds the other ports. The design is
// recorded in the repository's docs/adrs.
package mydi

//go:generate go run ./src/internal/forwards
