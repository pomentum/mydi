package mydi_test

import (
	"github.com/pomentum/mydi"
	"testing"
)

// The machine's own devices: listed, and an output opened and sent All Notes Off on channel 1, which sounds nothing.
func TestWindowsListsItsDevicesAndOpensAnOutput(t *testing.T) {
	ports, err := mydi.System{}.Ports()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ports: %+v", ports)
	for _, port := range ports {
		if !port.Out {
			continue
		}
		out, err := mydi.System{}.OpenOut(port.ID)
		if err != nil {
			t.Fatalf("%s: %v", port.ID, err)
		}
		if err := out.Write([]byte{0xB0, 123, 0}); err != nil {
			t.Fatalf("%s: %v", port.ID, err)
		}
		if err := out.Write([]byte{0xF0, 0x7E, 0xF7}); err == nil {
			t.Fatal("a system exclusive was taken on Windows")
		}
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Skip("this machine has no MIDI output")
}
