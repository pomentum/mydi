# Decisions

mydi's architecture decision records, one file per decision, written as [ADR-0000](0000-record-architecture-decisions.md) says, from
[template.md](template.md). Code cites a decision by its number and part (`ADR-0003 part D`). The evidence each names is a test of the
package or a report in [docs/reports](../reports/README.md).

| ADR | Decision | Status |
|---|---|---|
| [0000](0000-record-architecture-decisions.md) | Record architecture decisions, with parts, statuses and labelled evidence | Accepted |
| [0001](0001-mydi-a-go-midi-library-of-its-own.md) | mydi, a Go MIDI library of its own: its module and versions, its code in `src/` forwarded at the root, pure Go, its public API, tests at two levels, the measuring tool | Accepted (`v0.1.0`) |
| [0002](0002-events-ports-and-held-notes.md) | Events as JSON, one MIDI 1.0 message each, played at their time; held notes released per owner; ALSA, WinMM and virtual ports; a USB device's port by its identity | Accepted (A, D not this library's) |
| [0003](0003-one-time-thread.md) | One time thread owns every timed write, on the system's own clock and timer, at a priority the system serves on time; stalls counted | A to D Accepted; E Proposed |
| [0004](0004-following-a-devices-clock.md) | Following a device's MIDI clock: the system's stamps, a robust line, the count kept from Start to Stop, listeners told the transport and tempo | Accepted; amended by 0005 |
| [0005](0005-a-followed-clock-that-moves.md) | A followed clock that moves is sent on from the source's own pulses; the echo test a loop guard; two criteria per pulse | Accepted |
| [0006](0006-a-writer-per-port.md) | A driver that holds a write holds only its port; what waited released by its kind; a stuck call given up; the process above normal | C, D, F Accepted; A, B, E superseded by 0007 |
| [0007](0007-writes-handed-ahead.md) | Every port its own writer from the start, each timed write handed about 2 ms ahead | Accepted |
| [0008](0008-a-landing-that-stands-down.md) | Each writer lands its writes on their time, standing down while it lands late | Accepted |
