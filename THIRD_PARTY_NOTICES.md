# Third-party notices

mydi is licensed under Apache-2.0. It uses the software below, each under its own licence. Every dependency is pinned in `go.mod` and
`go.sum`; adding one adds its row here in the same commit.

| Module | Version | Licence | Where | What for |
|---|---|---|---|---|
| `golang.org/x/sys` | 0.50.0 | BSD-3-Clause | compiled into the library and `mydi` | Windows calls (`Winmm.dll`, the performance counter, waitable timers, MMCSS) and the Windows service protocol (`windows/svc`) for `mydi timing --service`; the Go project's own sub-repository |

The tests and the CI use the Go toolchain's image `golang:1.27.1-bookworm` (BSD-3-Clause for Go; Debian's packages under their own
licences) and the GitHub actions `actions/checkout` and `actions/upload-artifact` (MIT); none is distributed with mydi.
