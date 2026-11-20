package mydi_test

import (
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// slowPort is one output whose every write takes a steady time before it returns, as a write from session 0 did (about 1 ms, 2026-11-17),
// and one write, when asked, much longer.
type slowPort struct {
	took time.Duration
	hold chan time.Duration
}

type slowOut struct{ p *slowPort }

func (p *slowPort) Ports() ([]mydi.PortInfo, error) {
	return []mydi.PortInfo{{ID: "synth", Name: "Synth", Out: true}}, nil
}
func (p *slowPort) OpenOut(string) (mydi.Output, error)             { return &slowOut{p}, nil }
func (p *slowPort) OpenIn(string, func([]byte)) (mydi.Input, error) { return nil, mydi.ErrNoPort }
func (o *slowOut) Close() error                                     { return nil }
func (o *slowOut) Write([]byte) error {
	took := o.p.took
	select {
	case extra := <-o.p.hold:
		took += extra
	default:
	}
	// spun, not slept, so the steady delay is what it says on any system's timer
	for end := time.Now().Add(took); time.Now().Before(end); {
	}
	return nil
}

func TestASteadyWriteDelayIsLatencyNotLateness(t *testing.T) {
	// every write returns 2 ms after it starts: the port's delay is followed and left out of the late counts, which a service's status
	// showed as a third of its pulses late while its spread was 0.87 ms at the 99th percentile (2026-11-17); a hold still counts
	p := &slowPort{took: 2 * time.Millisecond, hold: make(chan time.Duration, 1)}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 240, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.Timing().Pulses >= 300 })
	tm := h.Timing()
	// the delay followed is the port's 2 ms and what the machine adds when it wakes the writer late: on a quiet machine its wake jitter is
	// a fraction of a millisecond, in a busy container on a laptop one or two, so the bound grows with what the hub measured of it
	if len(tm.Ports) != 1 {
		t.Fatalf("one port expected: %+v", tm.Ports)
	}
	slack := max(1, 2*tm.Ports[0].WakeJitterMs)
	if tm.Ports[0].WriteDelayMs < 1.5 || tm.Ports[0].WriteDelayMs > 2+slack {
		t.Fatalf("the steady delay not followed: %+v", tm.Ports)
	}
	// counted from the grid, every pulse would be more than 1 ms late; past the steady delay, only the machine's own late wakes are
	if tm.Late1ms > tm.Pulses/2 {
		t.Fatalf("%d of %d pulses counted late for a steady 2 ms delay", tm.Late1ms, tm.Pulses)
	}
	before := tm.Late5ms
	p.hold <- 40 * time.Millisecond
	waitFor(t, func() bool { return h.Timing().Late5ms > before })
	if d := h.Timing().Ports[0].WriteDelayMs; d > 2+slack {
		t.Fatalf("a hold of 40 ms moved the steady delay to %.2f ms", d)
	}
	_ = h.Clock("synth", 0, false)
}
