package mydi_test

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// heldPort is one output whose driver can be made to hold a write until let go, recording each message and when it was written.
type heldPort struct {
	mu      sync.Mutex
	block   chan struct{}
	written [][]byte
	times   []int64
	opens   int
}

type heldOut struct{ p *heldPort }

func (p *heldPort) Ports() ([]mydi.PortInfo, error) {
	return []mydi.PortInfo{{ID: "synth", Name: "Synth", Out: true}}, nil
}
func (p *heldPort) OpenOut(string) (mydi.Output, error) {
	p.mu.Lock()
	p.opens++
	p.mu.Unlock()
	return &heldOut{p}, nil
}
func (p *heldPort) OpenIn(string, func([]byte)) (mydi.Input, error) { return nil, mydi.ErrNoPort }
func (o *heldOut) Write(m []byte) error {
	o.p.mu.Lock()
	block := o.p.block
	o.p.mu.Unlock()
	if block != nil {
		<-block
	}
	o.p.mu.Lock()
	o.p.written = append(o.p.written, append([]byte(nil), m...))
	o.p.times = append(o.p.times, mydi.Now())
	o.p.mu.Unlock()
	return nil
}
func (o *heldOut) Close() error { return nil }

func (p *heldPort) hold() chan struct{} {
	release := make(chan struct{})
	p.mu.Lock()
	p.block = release
	p.mu.Unlock()
	return release
}

func (p *heldPort) letGo(release chan struct{}) {
	p.mu.Lock()
	p.block = nil
	p.mu.Unlock()
	close(release)
}

func (p *heldPort) after(n int) ([][]byte, []int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.written[n:]...), append([]int64(nil), p.times[n:]...)
}

func (p *heldPort) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.written)
}

func TestAShortHoldIsCaughtUpNeverInABurst(t *testing.T) {
	// a clock at 120 BPM held 100 ms (under a beat): the pulses that waited are written late, none closer than a quarter interval, the
	// count kept and no resync
	p := &heldPort{}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 120, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.count() > 12 })
	release := p.hold()
	time.Sleep(100 * time.Millisecond) // the hold is what the test is about
	from := p.count()
	p.letGo(release)
	waitFor(t, func() bool { return p.count() > from+24 })
	written, times := p.after(from)
	quarter := int64(time.Minute) / (120 * 24) / 4
	for i := 1; i < len(times); i++ {
		if bytes.Equal(written[i], []byte{0xF8}) && bytes.Equal(written[i-1], []byte{0xF8}) && times[i]-times[i-1] < quarter-int64(time.Millisecond) {
			t.Fatalf("pulses %.2f ms apart after the hold, under a quarter interval", float64(times[i]-times[i-1])/1e6)
		}
	}
	for _, m := range written {
		if m[0] == 0xF2 || m[0] == 0xFB {
			t.Fatalf("a resync after a short hold: % X", written)
		}
	}
	_ = h.Clock("synth", 0, false)
}

func TestALongHoldIsResynchronisedBySongPosition(t *testing.T) {
	// a clock at 240 BPM held 600 ms (past a beat of 250 ms): the backlog not replayed; Stop, the Song Position of the next sixteenth the
	// count reaches, and Continue on it, then the pulses go on
	p := &heldPort{}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 240, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.count() > 12 })
	release := p.hold()
	time.Sleep(600 * time.Millisecond) // the hold is what the test is about
	from := p.count()
	p.letGo(release)
	waitFor(t, func() bool {
		written, _ := p.after(from)
		for _, m := range written {
			if bytes.Equal(m, []byte{0xFB}) {
				return true
			}
		}
		return false
	})
	written, _ := p.after(from)
	// the one write held, then Stop, Song Position, and Continue just before a pulse
	stop, position, cont := -1, -1, -1
	for i, m := range written {
		switch {
		case bytes.Equal(m, []byte{0xFC}) && stop < 0:
			stop = i
		case m[0] == 0xF2 && position < 0:
			position = i
		case bytes.Equal(m, []byte{0xFB}) && cont < 0:
			cont = i
		}
	}
	if !(stop >= 0 && stop < position && position < cont) {
		t.Fatalf("no Stop, Song Position, Continue in order: % X", written)
	}
	pulses := 0
	for _, m := range written[:stop] {
		if bytes.Equal(m, []byte{0xF8}) {
			pulses++
		}
	}
	if pulses > 2 {
		t.Fatalf("%d pulses of the backlog replayed before the resync", pulses)
	}
	tm := h.Timing()
	if len(tm.Ports) != 1 || tm.Ports[0].Resyncs != 1 || tm.Ports[0].Skipped == 0 {
		t.Fatalf("%+v", tm.Ports)
	}
	t.Logf("resynchronised at sixteenth %d, %d pulses left out", int(written[position][1])|int(written[position][2])<<7, tm.Ports[0].Skipped)
	_ = h.Clock("synth", 0, false)
}

func TestHoldsInARowPastABeatAreResynchronisedToo(t *testing.T) {
	// a clock at 240 BPM (a beat of 250 ms) whose writes are held 150 ms each, five in a row: no one hold reaches a beat, but the clock falls
	// about 750 ms behind, so it is put back in place as after one long hold, never caught up in a burst (a 732 ms gap of such holds went
	// out as a burst in an hour's run, 2026-11-17)
	p := &heldPort{}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 240, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.count() > 12 })
	from := p.count()
	for i := 0; i < 5; i++ {
		release := p.hold()
		time.Sleep(150 * time.Millisecond) // the holds are what the test is about
		p.letGo(release)
	}
	waitFor(t, func() bool {
		written, _ := p.after(from)
		for _, m := range written {
			if bytes.Equal(m, []byte{0xFB}) {
				return true
			}
		}
		return false
	})
	if tm := h.Timing(); len(tm.Ports) != 1 || tm.Ports[0].Resyncs == 0 {
		t.Fatalf("no resync after holds in a row: %+v", tm.Ports)
	}
	_ = h.Clock("synth", 0, false)
}

// resyncOf runs a clock at this tempo, holds its writes this long, and answers what was written after the hold and when, waiting for the
// Continue when one is wanted.
func resyncOf(t *testing.T, bpm float64, hold time.Duration, wantContinue bool) ([][]byte, []int64, mydi.Timing) {
	t.Helper()
	p := &heldPort{}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", bpm, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.count() > 12 })
	release := p.hold()
	time.Sleep(hold) // the hold is what the test is about
	from := p.count()
	p.letGo(release)
	waitFor(t, func() bool {
		written, _ := p.after(from)
		if !wantContinue {
			return len(written) > 24
		}
		for _, m := range written {
			if bytes.Equal(m, []byte{0xFB}) {
				return true
			}
		}
		return false
	})
	written, times := p.after(from)
	tm := h.Timing()
	_ = h.Clock("synth", 0, false)
	return written, times, tm
}

func TestAResyncLimitUnderABeatResynchronisesAShorterHold(t *testing.T) {
	// a clock at 120 BPM (a beat of 500 ms) held 300 ms: caught up by default, put back in place when the limit is 150 ms (a device that runs
	// on its own clock past about 150 ms without one counts a catch-up twice, 2026-11-17)
	after, pause := mydi.ResyncDefaults()
	defer mydi.SetResync(after, pause)
	mydi.SetResync(0, 0)
	if _, _, tm := resyncOf(t, 120, 300*time.Millisecond, false); tm.Ports[0].Resyncs != 0 {
		t.Fatalf("a hold under a beat resynchronised with no limit set: %+v", tm.Ports)
	}
	mydi.SetResync(150*time.Millisecond, 0)
	if _, _, tm := resyncOf(t, 120, 300*time.Millisecond, true); tm.Ports[0].Resyncs != 1 {
		t.Fatalf("a hold past the limit of 150 ms not resynchronised: %+v", tm.Ports)
	}
}

func TestAResyncsContinueWaitsItsPauseAfterTheSongPosition(t *testing.T) {
	// with a pause of 200 ms the Continue goes on the first sixteenth at least that far after the Song Position, for a device that relocates
	// late (one played its notes 104 to 146 ms late with the two together, 2026-11-17)
	after, pause := mydi.ResyncDefaults()
	defer mydi.SetResync(after, pause)
	mydi.SetResync(0, 200*time.Millisecond)
	written, times, _ := resyncOf(t, 240, 600*time.Millisecond, true)
	position, cont := -1, -1
	for i, m := range written {
		switch {
		case m[0] == 0xF2 && position < 0:
			position = i
		case bytes.Equal(m, []byte{0xFB}) && cont < 0:
			cont = i
		}
	}
	if position < 0 || cont < position {
		t.Fatalf("no Song Position then Continue: % X", written)
	}
	if gap := time.Duration(times[cont] - times[position]); gap < 195*time.Millisecond {
		t.Fatalf("the Continue %v after the Song Position, under the pause of 200 ms", gap)
	}
	for _, m := range written[position+1 : cont] {
		if bytes.Equal(m, []byte{0xF8}) {
			t.Fatalf("a pulse written between the Song Position and the Continue: % X", written)
		}
	}
}

func TestAHoldPastTheLimitCanDropTheOwedPulsesWithNoTransport(t *testing.T) {
	// with the drop chosen, a clock held 300 ms past a limit of 150 ms writes no Stop, Song Position or Continue and does not replay what
	// it owed: it goes on from its grid, for a receiver that ran on its own clock through the gap
	after, pause := mydi.ResyncDefaults()
	defer mydi.SetResync(after, pause)
	defer mydi.SetHoldDrop(false)
	mydi.SetResync(150*time.Millisecond, 0)
	mydi.SetHoldDrop(true)
	written, _, tm := resyncOf(t, 120, 300*time.Millisecond, false)
	for _, m := range written {
		if m[0] == 0xFC || m[0] == 0xF2 || m[0] == 0xFB {
			t.Fatalf("transport written after a dropped hold: % X", written)
		}
	}
	if p := tm.Ports[0]; p.Resyncs != 0 || p.Skipped == 0 {
		t.Fatalf("%+v", p)
	}
}

func TestAHeldPortDropsStaleNotesAndKeepsWhatEndsThem(t *testing.T) {
	// note-ons that waited past 50 ms are dropped; their note offs are written
	p := &heldPort{}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Send("owner", "synth", []mydi.Event{{Type: "noteOn", Channel: 1, Note: intp(40), Velocity: intp(90)}}); err != nil {
		t.Fatal(err)
	}
	release := p.hold()
	_ = h.Send("owner", "synth", []mydi.Event{{Type: "noteOff", Channel: 1, Note: intp(40), At: 1}})
	_ = h.Send("owner", "synth", []mydi.Event{{Type: "noteOn", Channel: 1, Note: intp(41), Velocity: intp(90), At: 1}})
	_ = h.Send("owner", "synth", []mydi.Event{{Type: "noteOff", Channel: 1, Note: intp(41), At: 2}})
	time.Sleep(150 * time.Millisecond) // the hold is what the test is about
	from := p.count()
	p.letGo(release)
	waitFor(t, func() bool { written, _ := p.after(from); return len(written) >= 2 })
	written, _ := p.after(from)
	for _, m := range written {
		if bytes.Equal(m, []byte{0x90, 41, 90}) {
			t.Fatalf("a note-on 150 ms late was played: % X", written)
		}
	}
	if !bytes.Equal(written[0], []byte{0x80, 40, 0}) || !bytes.Equal(written[1], []byte{0x80, 41, 0}) {
		t.Fatalf("the note offs not both written: % X", written)
	}
}

func TestPanicGoesFirstOnAHeldPort(t *testing.T) {
	// a port held with note-ons waiting: panic is written first when it returns, and the note-ons that waited are not played
	p := &heldPort{}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Send("owner", "synth", []mydi.Event{{Type: "noteOn", Channel: 2, Note: intp(50), Velocity: intp(90)}}); err != nil {
		t.Fatal(err)
	}
	release := p.hold()
	_ = h.Send("owner", "synth", []mydi.Event{{Type: "program", Channel: 2, Program: intp(5), At: 1}})
	_ = h.Send("owner", "synth", []mydi.Event{{Type: "noteOn", Channel: 2, Note: intp(52), Velocity: intp(90), At: 1}})
	time.Sleep(20 * time.Millisecond) // both handed to the held writer
	from := p.count()
	if err := h.Panic("synth"); err != nil {
		t.Fatal(err)
	}
	p.letGo(release)
	waitFor(t, func() bool { written, _ := p.after(from); return len(written) >= 18 })
	written, _ := p.after(from)
	// the one write the driver held was the program change's; then panic: a note off for each note held (the one dropped too, harmless),
	// in any order, and All Notes Off on every channel, before anything else
	if !bytes.Equal(written[0], []byte{0xC1, 5}) {
		t.Fatalf("the held write was not the program change: % X", written)
	}
	i, off50 := 1, false
	for ; i < len(written) && written[i][0] == 0x81; i++ {
		off50 = off50 || bytes.Equal(written[i], []byte{0x81, 50, 0})
	}
	if !off50 || i+16 > len(written) || !bytes.Equal(written[i], []byte{0xB0, 123, 0}) {
		t.Fatalf("panic not first: % X", written)
	}
	for _, m := range written {
		if bytes.Equal(m, []byte{0x91, 52, 90}) {
			t.Fatalf("a note-on that waited was played after panic: % X", written)
		}
	}
}

func TestADriverCallThatNeverReturnsIsGivenUpAndThePortOpenedAgain(t *testing.T) {
	// a write that never returns: past the limit the writer is given up, its handle never closed, and the port opened afresh on its next
	// use; the other port's writes, and this one's afterwards, go on
	p := &heldPort{}
	h := mydi.NewHub(p, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Send("owner", "synth", []mydi.Event{{Type: "noteOn", Channel: 1, Note: intp(60), Velocity: intp(1)}}); err != nil {
		t.Fatal(err)
	}
	stuck := p.hold()
	_ = h.Send("owner", "synth", []mydi.Event{{Type: "noteOff", Channel: 1, Note: intp(60), At: 1}})
	waitFor(t, func() bool { return h.Timing().BlockedMs > 50 })
	mydi.SetStuckAfter(100 * time.Millisecond)
	defer mydi.SetStuckAfter(0)
	time.Sleep(150 * time.Millisecond) // past the limit is what the test is about
	p.mu.Lock()
	p.block = nil // a later write returns; the one held never does
	p.mu.Unlock()
	if err := h.Send("owner", "synth", []mydi.Event{{Type: "noteOn", Channel: 1, Note: intp(62), Velocity: intp(1)}}); err == nil {
		t.Fatal("a port whose writer is stuck took a write")
	}
	if err := h.Send("owner", "synth", []mydi.Event{{Type: "noteOn", Channel: 1, Note: intp(64), Velocity: intp(1)}}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	opens := p.opens
	p.mu.Unlock()
	tm := h.Timing()
	abandoned := uint64(0)
	for _, port := range tm.Ports {
		abandoned += port.Abandoned
	}
	if opens != 2 || abandoned != 1 {
		t.Fatalf("opened %d times, %d writers given up", opens, abandoned)
	}
	_ = stuck // left held: its thread is the one given up
}
