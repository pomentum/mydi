package mydi_test

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/pomentum/mydi"
)

// changingPorts is a machine whose MIDI ports a test plugs in and pulls; it opens none.
type changingPorts struct {
	mu  sync.Mutex
	ids []string
}

func (p *changingPorts) set(ids ...string) {
	p.mu.Lock()
	p.ids = ids
	p.mu.Unlock()
}
func (p *changingPorts) Ports() ([]mydi.PortInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ports []mydi.PortInfo
	for _, id := range p.ids {
		ports = append(ports, mydi.PortInfo{ID: id, Name: id, Out: true})
	}
	return ports, nil
}
func (p *changingPorts) OpenOut(string) (mydi.Output, error)             { return nil, mydi.ErrNoPort }
func (p *changingPorts) OpenIn(string, func([]byte)) (mydi.Input, error) { return nil, mydi.ErrNoPort }

func TestADeviceThatComesOrGoesIsLoggedAndKept(t *testing.T) {
	// any device of the machine counts, used by the hub or not: on Windows one that comes or goes holds the other ports' writes for about
	// 30 s (2026-11-17), so the program logs it and says until when
	machine := &changingPorts{ids: []string{"synth", "keys"}}
	var mu sync.Mutex
	var lines []string
	h := mydi.NewHub(machine, only("synth"), nil, func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	})
	defer h.Close()
	h.Listen()
	if d := h.Devices(); !d.At.IsZero() {
		t.Fatalf("the first look taken for a change: %+v", d)
	}
	machine.set("synth", "drums")
	h.Listen()
	d := h.Devices()
	if strings.Join(d.Came, ",") != "drums" || strings.Join(d.Left, ",") != "keys" || d.At.IsZero() {
		t.Fatalf("%+v", d)
	}
	if onWindows := runtime.GOOS == "windows"; onWindows != !d.HoldsUntil.IsZero() || (onWindows && d.HoldsUntil.Sub(d.At) != mydi.DeviceHold) {
		t.Fatalf("holds until %v on %s", d.HoldsUntil, runtime.GOOS)
	}
	mu.Lock()
	logged := strings.Join(lines, "\n")
	mu.Unlock()
	if !strings.Contains(logged, "the machine's ports changed: drums came; keys left") {
		t.Fatalf("not logged:\n%s", logged)
	}
	// nothing changed: the last change kept, nothing logged again
	before := len(lines)
	h.Listen()
	if h.Devices().At != d.At || len(lines) != before {
		t.Fatalf("a look with no change counted as one")
	}
}
