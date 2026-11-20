# AGENTS.md

The rules for this repository are in [DEVELOPMENT.md](DEVELOPMENT.md); do not repeat them here, change that file instead. The hard ones:

- The code is in `src/`; the root's `mydi.go` forwards it and is generated (`go generate` at the root after an exported name changes).
- Pure Go, no cgo; the standard library and `golang.org/x/sys` only. A new dependency needs an ADR and a row in
  `THIRD_PARTY_NOTICES.md` in the same commit.
- The time thread never waits on a driver and allocates nothing per pulse; a change to timing, writers or following gets an ADR in
  `docs/adrs/` before the code, with its evidence.
- Tests are black-box (`package mydi_test`), live in `tests/`, and use recordings or virtual ports, never the machine's own MIDI ports. Measures
  (`MYDI_TIMING=1`) run only when asked.
- Check with `gofmt -l .`, `go vet ./...` for Linux and Windows, and `go test ./...` in the pinned Go image (DEVELOPMENT.md, Commands).
- Nothing private: no machine names, device owners' setups, local paths or credentials anywhere, commit messages included.
- Commits are the owner's: no AI co-author line or attribution. Conventional commits, lower case. Never rebase; merge.
- No push, tag or release without the owner's approval in the current conversation.
