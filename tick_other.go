//go:build !windows

package mydi

import "time"

// SystemTick answers nothing where the system has no timer resolution to read.
func SystemTick() (now, coarsest, finest time.Duration, ok bool) { return 0, 0, 0, false }
