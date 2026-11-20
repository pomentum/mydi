package mydi_test

import (
	"sync"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// twoPorts is a driver with two outputs, a and b, each recording when it was written; a write to a port waits while its block is set,
// as a driver does that holds a write (a software synth while the system rebuilds its devices: up to 961 ms, 2026-11-15).
type twoPorts struct {
	mu    sync.Mutex
	block map[string]chan struct{}
	times map[string][]int64
}

type twoPortsOut struct {
	d  *twoPorts
	id string
}

func (d *twoPorts) Ports() ([]mydi.PortInfo, error) {
	return []mydi.PortInfo{{ID: "a", Name: "A", Out: true}, {ID: "b", Name: "B", Out: true}}, nil
}
func (d *twoPorts) OpenOut(id string) (mydi.Output, error) { return &twoPortsOut{d, id}, nil }
func (d *twoPorts) OpenIn(string, func([]byte)) (mydi.Input, error) {
	return nil, mydi.ErrNoPort
}
func (o *twoPortsOut) Write([]byte) error {
	o.d.mu.Lock()
	block := o.d.block[o.id]
	o.d.mu.Unlock()
	if block != nil {
		<-block
	}
	o.d.mu.Lock()
	o.d.times[o.id] = append(o.d.times[o.id], mydi.Now())
	o.d.mu.Unlock()
	return nil
}
func (o *twoPortsOut) Close() error { return nil }

func (d *twoPorts) written(id string) []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int64(nil), d.times[id]...)
}

func TestAPortWhoseDriverHoldsAWriteHoldsNoOtherPort(t *testing.T) {
	// two ports clocked at 120 BPM; a's driver holds its writes for 300 ms. b's clock must not wait for it, where one time thread writing
	// every port held b as long as a was held (301 ms): its longest gap stays near its interval (20.8 ms; 22.8 ms seen, and once 51.6 ms
	// in a container on Go's own timers at normal priority), so the bound is a third of the hold; Windows's own is measured natively
	// (ADR-0006 part E)
	d := &twoPorts{block: map[string]chan struct{}{}, times: map[string][]int64{}}
	h := mydi.NewHub(d, only("a", "b"), nil, nil)
	defer h.Close()
	for _, id := range []string{"a", "b"} {
		if err := h.Clock(id, 120, true); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return len(d.written("b")) > 24 })
	release := make(chan struct{})
	d.mu.Lock()
	d.block["a"] = release
	d.mu.Unlock()
	from := mydi.Now()
	time.Sleep(300 * time.Millisecond) // the hold itself is what the test is about
	d.mu.Lock()
	d.block["a"] = nil
	d.mu.Unlock()
	close(release)
	until := mydi.Now()
	waitFor(t, func() bool {
		b := d.written("b")
		return len(b) > 0 && b[len(b)-1] > until+int64(100*time.Millisecond)
	})
	longest := int64(0)
	b := d.written("b")
	for i := 1; i < len(b); i++ {
		if b[i] > from && b[i-1] < until {
			longest = max(longest, b[i]-b[i-1])
		}
	}
	if longest > int64(100*time.Millisecond) {
		t.Fatalf("while a was held 300 ms, b's longest gap was %.1f ms", float64(longest)/1e6)
	}
	t.Logf("while a was held 300 ms, b's longest gap was %.1f ms", float64(longest)/1e6)
	for _, id := range []string{"a", "b"} {
		_ = h.Clock(id, 0, false)
	}
}
