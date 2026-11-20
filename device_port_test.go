package mydi_test

import (
	"testing"

	"github.com/pomentum/mydi"
)

// A MIDI target named by its device's identity plays the port the system made of it: on Linux by the raw device below
// it, its card's id naming the port; on Windows by the device's name, only when one port holds it.
func TestADevicesPortIsFoundByWhatTheSystemMadeOfIt(t *testing.T) {
	ports := []mydi.PortInfo{
		{ID: mydi.IDOf("A49 0"), Name: "A49 0"},
		{ID: mydi.IDOf("hw:2,0"), Name: "hw:2,0"},
		{ID: mydi.IDOf("Drum machine"), Name: "Drum machine"},
		{ID: mydi.IDOf("2- USB MIDI"), Name: "2- USB MIDI"},
		{ID: mydi.IDOf("3- USB MIDI"), Name: "3- USB MIDI"},
	}
	if id, ok := mydi.PortOfDevice(ports, "1", "0", "A49", "A-49"); !ok || id != mydi.IDOf("A49 0") {
		t.Fatal(id, ok)
	}
	if id, ok := mydi.PortOfDevice(ports, "2", "0", "", ""); !ok || id != mydi.IDOf("hw:2,0") {
		t.Fatal(id, ok)
	}
	if _, ok := mydi.PortOfDevice(ports, "3", "0", "Gone", ""); ok {
		t.Fatal("a card with no port found")
	}
	if id, ok := mydi.PortOfDevice(ports, "", "", "", "drum machine"); !ok || id != mydi.IDOf("Drum machine") {
		t.Fatal(id, ok)
	}
	// two of the same interface: no guess
	if _, ok := mydi.PortOfDevice(ports, "", "", "", "USB MIDI"); ok {
		t.Fatal("one of two identical ports guessed")
	}
	if _, ok := mydi.PortOfDevice(ports, "", "", "", ""); ok {
		t.Fatal("a device with no name found a port")
	}
}
