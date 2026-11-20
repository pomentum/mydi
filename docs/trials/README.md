# Trials

A trial is a use mydi sets out to cover: what a program must be able to do with it, and how that is shown. Work is taken on when a
trial needs it. To add one, copy [template.md](template.md) to `TST-NNNN.md` with the next free code, write its target, and add its row.

| Trial | Use | Target | Status |
|---|---|---|---|
| [TST-0001](TST-0001.md) | Play and clock a synth | A program plays notes and clocks a synth from Go, on time on Windows and Linux, and leaves no note stuck when it goes | Covered |
| [TST-0002](TST-0002.md) | Follow a drum machine | A drum machine's clock followed and sent on to a synth, every pulse counted, its tempo and bars told | Covered |
| [TST-0003](TST-0003.md) | Measure a machine | Anyone measures their own machine's MIDI timing and where its system stamps input, with no extra hardware | Covered |
| [TST-0004](TST-0004.md) | A device that holds | A device whose driver holds writes, or that comes and goes, troubles only its own port | Covered (Windows) |
