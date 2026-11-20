package mydi_test

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// rig is a stand-in machine with a device that plays its clock in (daw) and two that take one (synth and drum), each output recording what
// it is written.
type rig struct {
	mu      sync.Mutex
	written map[string][][]byte
	stamped func(mydi.Stamped)
	events  []mydi.Event
}

type rigOut struct {
	r  *rig
	id string
}

func (r *rig) Ports() ([]mydi.PortInfo, error) {
	return []mydi.PortInfo{{ID: "daw", Name: "DAW", In: true, Out: true}, {ID: "synth", Name: "Synth", In: true, Out: true},
		{ID: "drum", Name: "Drum", In: true, Out: true}}, nil
}
func (r *rig) OpenOut(id string) (mydi.Output, error) { return &rigOut{r, id}, nil }
func (r *rig) OpenIn(string, func([]byte)) (mydi.Input, error) {
	return r, nil
}
func (r *rig) OpenInStamped(_ string, deliver func(mydi.Stamped)) (mydi.Input, error) {
	r.mu.Lock()
	r.stamped = deliver
	r.mu.Unlock()
	return r, nil
}
func (r *rig) Close() error { return nil }
func (o *rigOut) Write(m []byte) error {
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	if o.r.written == nil {
		o.r.written = map[string][][]byte{}
	}
	o.r.written[o.id] = append(o.r.written[o.id], append([]byte(nil), m...))
	return nil
}
func (o *rigOut) Close() error { return nil }

// count is how many of a port's messages begin with this status.
func (r *rig) count(id string, status byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.written[id] {
		if m[0] == status {
			n++
		}
	}
	return n
}

func (r *rig) heard(e mydi.Received) {
	if e.Event.Type != "clock" || e.Port != "daw" {
		return
	}
	r.mu.Lock()
	r.events = append(r.events, e.Event)
	r.mu.Unlock()
}

func (r *rig) told() []mydi.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mydi.Event(nil), r.events...)
}

// play plays a song into the device's input in real time, from a little after now.
func (r *rig) play(song []heard) {
	start := mydi.Now() + int64(20*time.Millisecond)
	for _, h := range song {
		at := start + h.at
		if wait := time.Duration(at - mydi.Now()); wait > 0 {
			time.Sleep(wait)
		}
		r.mu.Lock()
		deliver := r.stamped
		r.mu.Unlock()
		deliver(mydi.Stamped{Message: h.m, Driver: at - at%ms, Received: mydi.Now()})
	}
}

// song is a steady clock at a tempo, its first pulse a second in, with Start before it and Stop after it.
func song(beats int, bpm float64) []heard {
	times := newSource().play(24*beats, bpm, nil).times
	base := times[0] - int64(time.Second)
	for i := range times {
		times[i] -= base
	}
	return transport(times)
}

func newRig() (*rig, *mydi.Hub) {
	r := &rig{}
	return r, mydi.NewHub(r, only("daw", "synth", "drum"), r.heard, nil)
}

func TestAPortFollowsAnotherAndListenersHearTheSongsPlace(t *testing.T) {
	r, h := newRig()
	defer h.Close()
	if err := h.FollowFrom("synth", "daw", mydi.FollowOptions{Beats: "beat", Meter: "3/4"}); err != nil {
		t.Fatal(err)
	}
	r.play(song(7, 120))
	waitFor(t, func() bool { return r.count("synth", 0xFC) == 1 })
	if s, p := r.count("synth", 0xFA), r.count("synth", 0xF8); s != 1 || p != 24*7 {
		t.Fatalf("the synth: %d Starts, %d pulses for %d", s, p, 24*7)
	}
	waitFor(t, func() bool {
		events := r.told()
		return len(events) > 0 && events[len(events)-1].Clock == "stop"
	})
	events := r.told()
	var beats [][2]int
	var tempos []float64
	for _, e := range events {
		switch e.Clock {
		case "beat":
			beats = append(beats, [2]int{*e.Bar, *e.Beat})
		case "tempo":
			tempos = append(tempos, e.Tempo)
		}
	}
	// a steady source's tempo told once: the estimate's wander is not a change
	if events[0].Clock != "start" || len(tempos) != 1 || math.Abs(tempos[0]-120) > 0.5 {
		t.Fatalf("first %q, tempos told %v", events[0].Clock, tempos)
	}
	tempo := tempos[0]
	want := [][2]int{{1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}, {2, 3}, {3, 1}}
	if len(beats) != len(want) {
		t.Fatalf("beats %v", beats)
	}
	for i := range want {
		if beats[i] != want[i] {
			t.Fatalf("beats %v, want %v", beats, want)
		}
	}
	t.Logf("%d events told: start, tempo %.2f, seven beats in 3/4, stop", len(events), tempo)
}

func TestATempoChangedByTheSourceIsTold(t *testing.T) {
	r, h := newRig()
	defer h.Close()
	if err := h.FollowFrom("synth", "daw", mydi.FollowOptions{}); err != nil {
		t.Fatal(err)
	}
	times := newSource().play(24*4, 120, nil).play(24*6, 132, nil).times
	base := times[0] - int64(time.Second)
	for i := range times {
		times[i] -= base
	}
	r.play(transport(times))
	waitFor(t, func() bool { return r.count("synth", 0xFC) == 1 })
	var tempos []float64
	for _, e := range r.told() {
		if e.Clock == "tempo" {
			tempos = append(tempos, e.Tempo)
		}
	}
	if len(tempos) < 2 || len(tempos) > 4 || math.Abs(tempos[0]-120) > 0.5 || math.Abs(tempos[len(tempos)-1]-132) > 0.5 {
		t.Fatalf("tempos told %v for 120 then 132", tempos)
	}
	t.Logf("tempos told %v for 120 then 132", tempos)
}

func TestTwoPortsFollowOneSourceAndClockSetTakesEachBack(t *testing.T) {
	r, h := newRig()
	defer h.Close()
	for _, port := range []string{"synth", "drum"} {
		if err := h.FollowFrom(port, "daw", mydi.FollowOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	r.play(song(4, 120))
	waitFor(t, func() bool { return r.count("synth", 0xFC) == 1 && r.count("drum", 0xFC) == 1 })
	if a, b := r.count("synth", 0xF8), r.count("drum", 0xF8); a != 96 || b != 96 {
		t.Fatalf("pulses: synth %d, drum %d, for 96", a, b)
	}
	// no clock out of a port followed, nor into a source the hub clocks
	if err := h.Clock("daw", 120, true); !errors.Is(err, mydi.ErrFollowSamePort) {
		t.Fatalf("the source clocked: %v", err)
	}
	if err := h.FollowFrom("daw", "synth", mydi.FollowOptions{}); !errors.Is(err, mydi.ErrFollowSamePort) {
		t.Fatalf("the source made a follower: %v", err)
	}
	if err := h.FollowFrom("synth", "synth", mydi.FollowOptions{}); !errors.Is(err, mydi.ErrFollowSamePort) {
		t.Fatalf("a port following itself: %v", err)
	}
	// the synth back to its own clock, which runs; the drum stopped, and the following with it, the device's input listened to again
	if err := h.Clock("synth", 100, true); err != nil {
		t.Fatal(err)
	}
	if err := h.FollowFrom("drum", "synth", mydi.FollowOptions{}); !errors.Is(err, mydi.ErrFollowClocked) {
		t.Fatalf("following a port the hub clocks: %v", err)
	}
	if err := h.Clock("drum", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := h.Clock("daw", 120, true); err != nil {
		t.Fatalf("the source, followed by none now, clocked: %v", err)
	}
	waitFor(t, func() bool { return r.count("synth", 0xF8) > 96+4 })
	for _, bad := range []mydi.FollowOptions{{Meter: "5/5"}, {Beats: "every"}, {Hold: 65}} {
		if err := h.FollowFrom("drum", "synth", bad); err == nil || errors.Is(err, mydi.ErrFollowClocked) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	_ = h.Clock("synth", 0, false)
	_ = h.Clock("daw", 0, false)
}

func TestASourceLostIsToldAndASongPositionPlacesTheBars(t *testing.T) {
	r, h := newRig()
	defer h.Close()
	// held one beat when lost; bars told
	if err := h.FollowFrom("synth", "daw", mydi.FollowOptions{Hold: 1, Beats: "bar"}); err != nil {
		t.Fatal(err)
	}
	// two bars and the clock gone with no Stop: lost, held a beat, stopped
	s := song(8, 120)
	r.play(s[:len(s)-1])
	waitFor(t, func() bool { return r.count("synth", 0xFC) == 1 })
	// back: a Song Position of sixteen sixteenths (a bar of 4/4) while stopped, then Continue and a bar more
	position := []heard{{0, []byte{0xF2, 16, 0}}}
	more := song(4, 120)
	more[0] = heard{more[0].at, []byte{0xFB}}
	r.play(append(position, more...))
	waitFor(t, func() bool { return r.count("synth", 0xFC) == 2 })
	var said []string
	var bars []int
	for _, e := range r.told() {
		said = append(said, e.Clock)
		switch e.Clock {
		case "bar":
			bars = append(bars, *e.Bar)
		case "position":
			if *e.Position != 16 || *e.Bar != 2 || *e.Beat != 1 {
				t.Fatalf("position %d at bar %d beat %d", *e.Position, *e.Bar, *e.Beat)
			}
		}
	}
	has := func(what string) bool {
		for _, s := range said {
			if s == what {
				return true
			}
		}
		return false
	}
	if !has("lost") || !has("back") || !has("position") || !has("continue") {
		t.Fatalf("told %v", said)
	}
	// the hold's pulses are counted, and the followers had them: its beat began the third bar
	if len(bars) != 4 || bars[0] != 1 || bars[1] != 2 || bars[2] != 3 || bars[3] != 2 {
		t.Fatalf("bars %v: two, the hold's third, then the second again after the position", bars)
	}
	t.Logf("told %v", said)
}
