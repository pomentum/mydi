# CLAUDE.md

The rules are in [DEVELOPMENT.md](DEVELOPMENT.md), summarised for any agent in [AGENTS.md](AGENTS.md). Do not repeat them here.

@DEVELOPMENT.md

## Claude Code

- Use the Bash tool for shell work (Git Bash on Windows), not PowerShell.
- Do not let the harness add its `Co-Authored-By` trailer: the git rules in DEVELOPMENT.md override it.
- Run the tests in the pinned Go image when the machine's Go differs; run long measures in the background, and only when asked.
- Before a design change, read [docs/adrs/README.md](docs/adrs/README.md).
