package mydi

import "strings"

// PortOfDevice is the port the system made of a USB device a MIDI target names by its identity: on Linux the
// raw MIDI device the kernel put below it (card and device, the card's id naming the port as Ports does); on Windows, which keeps no
// such link, the one port whose name holds the device's own name, and none when two do, so a guess never plays the wrong synth.
func PortOfDevice(ports []PortInfo, card, device, cardID, name string) (string, bool) {
	if card != "" && device != "" {
		want := cardID + " " + device
		if cardID == "" {
			want = "hw:" + card + "," + device
		}
		id := IDOf(want)
		for _, p := range ports {
			if p.ID == id {
				return id, true
			}
		}
		return "", false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", false
	}
	found := ""
	for _, p := range ports {
		if strings.Contains(strings.ToLower(p.Name), name) {
			if found != "" && found != p.ID {
				return "", false
			}
			found = p.ID
		}
	}
	return found, found != ""
}
