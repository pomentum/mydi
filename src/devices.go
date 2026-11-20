package src

import (
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// DeviceHold is how long Windows may hold MIDI writes after a device comes or goes: Windows MIDI Services' "surprise removal", a second
// device unplugged holding the synth's writes up to 215 ms for about 30 s (2026-11-17, the clock report's known limitation).
const DeviceHold = 30 * time.Second

// DeviceChange is the last change of the machine's MIDI ports Listen saw: the ports that came and those that left, by id, and on Windows
// until when the system may hold MIDI writes for it (zero elsewhere). Every port counts, used by the hub or not: any device that comes or
// goes holds the others.
type DeviceChange struct {
	At         time.Time
	Came, Left []string
	HoldsUntil time.Time
}

// devices is what watchDevices keeps: the machine's ports last seen and the last change.
type devices struct {
	mu    sync.Mutex
	known map[string]bool
	last  DeviceChange
}

// watchDevices compares the machine's ports with those seen last and logs a change; the first look only learns them.
func (h *Hub) watchDevices(all []PortInfo) {
	now := map[string]bool{}
	for _, port := range all {
		now[port.ID] = true
	}
	h.devices.mu.Lock()
	known := h.devices.known
	h.devices.known = now
	if known == nil {
		h.devices.mu.Unlock()
		return
	}
	var came, left []string
	for id := range now {
		if !known[id] {
			came = append(came, id)
		}
	}
	for id := range known {
		if !now[id] {
			left = append(left, id)
		}
	}
	if len(came)+len(left) == 0 {
		h.devices.mu.Unlock()
		return
	}
	sort.Strings(came)
	sort.Strings(left)
	change := DeviceChange{At: time.Now(), Came: came, Left: left}
	if runtime.GOOS == "windows" {
		change.HoldsUntil = change.At.Add(DeviceHold)
	}
	h.devices.last = change
	h.devices.mu.Unlock()
	h.log("midi: the machine's ports changed: %s%s", describeChange(came, left), holdNote(change))
}

func describeChange(came, left []string) string {
	var parts []string
	if len(came) > 0 {
		parts = append(parts, strings.Join(came, ", ")+" came")
	}
	if len(left) > 0 {
		parts = append(parts, strings.Join(left, ", ")+" left")
	}
	return strings.Join(parts, "; ")
}

func holdNote(change DeviceChange) string {
	if change.HoldsUntil.IsZero() {
		return ""
	}
	return "; Windows may hold MIDI writes for about 30 s (plug every device in before a show and unplug none during it)"
}

// Devices answers the last change of the machine's MIDI ports, zero when none was seen since the program started.
func (h *Hub) Devices() DeviceChange {
	h.devices.mu.Lock()
	defer h.devices.mu.Unlock()
	return h.devices.last
}
