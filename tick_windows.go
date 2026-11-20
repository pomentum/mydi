//go:build windows

package mydi

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procQueryTimerResolution = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryTimerResolution")

// SystemTick answers the system's timer resolution now, the coarsest it may be (the default tick, 15.625 ms on most machines) and the
// finest, read without changing anything; ok is false where the system does not say. A process that raises the resolution moves now
// toward the finest; a machine left at the default tick reads now equal to the coarsest (ADR-0003 part B's question).
func SystemTick() (now, coarsest, finest time.Duration, ok bool) {
	var maximum, minimum, current uint32
	if r, _, _ := procQueryTimerResolution.Call(uintptr(unsafe.Pointer(&maximum)), uintptr(unsafe.Pointer(&minimum)),
		uintptr(unsafe.Pointer(&current))); r != 0 {
		return 0, 0, 0, false
	}
	hundreds := func(v uint32) time.Duration { return time.Duration(v) * 100 * time.Nanosecond }
	return hundreds(current), hundreds(maximum), hundreds(minimum), true
}
