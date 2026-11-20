package mydi_test

import (
	"bytes"
	"errors"
	"github.com/pomentum/mydi"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// intp is a number an event names, by its address.
func intp(v int) *int { return &v }

func TestEventsEncodeAsMIDISaysAndRefuseWhatDoesNotRead(t *testing.T) {
	cases := []struct {
		event mydi.Event
		want  []byte
	}{
		{mydi.Event{Type: "noteOn", Channel: 1, Note: intp(60), Velocity: intp(100)}, []byte{0x90, 60, 100}},
		{mydi.Event{Type: "noteOff", Channel: 16, Note: intp(60)}, []byte{0x8F, 60, 0}},
		{mydi.Event{Type: "cc", Channel: 2, Controller: intp(74), Value: intp(127)}, []byte{0xB1, 74, 127}},
		{mydi.Event{Type: "program", Channel: 10, Program: intp(5)}, []byte{0xC9, 5}},
		{mydi.Event{Type: "pitchBend", Channel: 1, Bend: intp(0)}, []byte{0xE0, 0x00, 0x40}},
		{mydi.Event{Type: "pitchBend", Channel: 1, Bend: intp(-8192)}, []byte{0xE0, 0x00, 0x00}},
		{mydi.Event{Type: "channelPressure", Channel: 3, Pressure: intp(9)}, []byte{0xD2, 9}},
		{mydi.Event{Type: "start"}, []byte{0xFA}},
		{mydi.Event{Type: "sysex", Data: []int{0x7E, 0x7F, 0x09, 0x01}}, []byte{0xF0, 0x7E, 0x7F, 0x09, 0x01, 0xF7}},
	}
	for _, c := range cases {
		got, err := mydi.Encode(c.event)
		if err != nil || !bytes.Equal(got, c.want) {
			t.Fatalf("%+v: % X, %v", c.event, got, err)
		}
		back, ok := mydi.Decode(got)
		if !ok || back.Type != c.event.Type {
			t.Fatalf("% X decoded as %+v", got, back)
		}
	}
	for _, bad := range []mydi.Event{
		{Type: "noteOn", Channel: 0, Note: intp(60), Velocity: intp(1)},
		{Type: "noteOn", Channel: 17, Note: intp(60), Velocity: intp(1)},
		{Type: "noteOn", Channel: 1, Note: intp(128), Velocity: intp(1)},
		{Type: "noteOn", Channel: 1, Velocity: intp(1)},
		{Type: "cc", Channel: 1, Controller: intp(7)},
		{Type: "pitchBend", Channel: 1, Bend: intp(9000)},
		{Type: "sysex", Data: []int{0xF7}},
		{Type: "noteOn", Channel: 1, Note: intp(60), Velocity: intp(1), At: 60001},
		{Type: "reboot"},
	} {
		if _, err := mydi.Encode(bad); err == nil {
			t.Fatalf("took %+v", bad)
		}
	}
	// a note on with velocity 0 is a note off
	if e, _ := mydi.Decode([]byte{0x90, 60, 0}); e.Type != "noteOff" {
		t.Fatalf("%+v", e)
	}
}

func TestTheParserKeepsRunningStatusAndLetsRealTimeThrough(t *testing.T) {
	var got [][]byte
	p := &mydi.Parser{}
	// a note on, two more by running status, a clock inside the third, a system exclusive, then a program change
	p.Feed([]byte{0x90, 60, 100, 62, 100, 64, 0xF8, 100, 0xF0, 0x7E, 0x01, 0xF7, 0xC0, 5}, func(m []byte) { got = append(got, m) })
	want := [][]byte{{0x90, 60, 100}, {0x90, 62, 100}, {0xF8}, {0x90, 64, 100}, {0xF0, 0x7E, 0x01, 0xF7}, {0xC0, 5}}
	if len(got) != len(want) {
		t.Fatalf("% X", got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d: % X", i, got[i])
		}
	}
}

// fake is a driver whose one port records what is written to it and plays in what the test says.
type fake struct {
	mu       sync.Mutex
	written  [][]byte
	fail     bool
	attempts int
	block    chan struct{} // a write waits on it while it is set: a driver that does not return
	deliver  func([]byte)
	stamped  func(mydi.Stamped)
	times    []int64 // when each message written was written
	// missing are ports unplugged now; openedIn and stampedIn the ports opened for input, each time, plainly and with stamps
	missing   map[string]bool
	openedIn  []string
	stampedIn []string
}

func (f *fake) Ports() ([]mydi.PortInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ports []mydi.PortInfo
	for _, p := range []mydi.PortInfo{{ID: "synth", Name: "Synth", In: true, Out: true}, {ID: "daw", Name: "DAW", In: true, Out: true}} {
		if !f.missing[p.ID] {
			ports = append(ports, p)
		}
	}
	return ports, nil
}
func (f *fake) OpenOut(id string) (mydi.Output, error) {
	if id != "synth" {
		return nil, mydi.ErrNoPort
	}
	return f, nil
}
func (f *fake) OpenIn(id string, deliver func([]byte)) (mydi.Input, error) {
	f.mu.Lock()
	f.openedIn = append(f.openedIn, id)
	f.deliver = deliver
	f.mu.Unlock()
	return f, nil
}
func (f *fake) OpenInStamped(id string, deliver func(mydi.Stamped)) (mydi.Input, error) {
	f.mu.Lock()
	if f.missing[id] {
		f.mu.Unlock()
		return nil, mydi.ErrNoPort
	}
	f.stampedIn = append(f.stampedIn, id)
	f.stamped = deliver
	f.mu.Unlock()
	return f, nil
}
func (f *fake) Write(m []byte) error {
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.fail {
		return errors.New("unplugged")
	}
	f.written = append(f.written, append([]byte(nil), m...))
	f.times = append(f.times, mydi.Now())
	return nil
}
func (f *fake) Close() error { return nil }
func (f *fake) messages() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.written...)
}
func (f *fake) has(m []byte) bool {
	for _, w := range f.messages() {
		if bytes.Equal(w, m) {
			return true
		}
	}
	return false
}

func only(ids ...string) func(string) bool {
	return func(id string) bool {
		for _, one := range ids {
			if one == id {
				return true
			}
		}
		return false
	}
}

func TestTheHubReleasesWhatACellHeldAndCancelsWhatItHadNotPlayed(t *testing.T) {
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Send("owner-a", "synth", []mydi.Event{
		{Type: "noteOn", Channel: 1, Note: intp(60), Velocity: intp(100)},
		{Type: "noteOn", Channel: 2, Note: intp(64), Velocity: intp(100)},
		{Type: "noteOff", Channel: 1, Note: intp(60), At: 5000},
	}); err != nil {
		t.Fatal(err)
	}
	_ = h.Send("owner-b", "synth", []mydi.Event{{Type: "noteOn", Channel: 3, Note: intp(67), Velocity: intp(90)}})
	h.Release("owner-a")
	// owner-a's notes released and its channels told All Notes Off; owner-b's note left alone
	for _, m := range [][]byte{{0x80, 60, 0}, {0x81, 64, 0}, {0xB0, 123, 0}, {0xB1, 123, 0}} {
		if !f.has(m) {
			t.Fatalf("no % X in % X", m, f.messages())
		}
	}
	if f.has([]byte{0x82, 67, 0}) {
		t.Fatal("another owner's note was released")
	}
	// its pending note off was cancelled with it
	before := len(f.messages())
	time.Sleep(50 * time.Millisecond)
	if len(f.messages()) != before {
		t.Fatal("something more was played")
	}
	// nothing half plays: one bad event refuses the call
	if err := h.Send("owner-a", "synth", []mydi.Event{{Type: "noteOn", Channel: 1, Note: intp(60), Velocity: intp(1)}, {Type: "nope"}}); err == nil {
		t.Fatal("a bad event was taken")
	}
	if len(f.messages()) != before {
		t.Fatal("half a call played")
	}
	// a port this hub does not use is refused
	if err := h.Send("owner-a", "daw", []mydi.Event{{Type: "start"}}); !errors.Is(err, mydi.ErrNotAllowed) {
		t.Fatalf("%v", err)
	}
}

func TestAScheduledEventIsPlayedAtItsTime(t *testing.T) {
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	start := time.Now()
	_ = h.Send("owner-a", "synth", []mydi.Event{{Type: "noteOn", Channel: 1, Note: intp(60), Velocity: intp(1), At: 80}})
	for len(f.messages()) == 0 && time.Since(start) < 2*time.Second {
		time.Sleep(time.Millisecond)
	}
	if took := time.Since(start); took < 75*time.Millisecond || len(f.messages()) == 0 {
		t.Fatalf("played after %s", took)
	}
}

func TestAPortThatFailsIsOpenedAgainAndToldAllNotesOff(t *testing.T) {
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	_ = h.Send("owner-a", "synth", []mydi.Event{{Type: "noteOn", Channel: 5, Note: intp(60), Velocity: intp(100)}})
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	if err := h.Send("owner-a", "synth", []mydi.Event{{Type: "noteOn", Channel: 5, Note: intp(61), Velocity: intp(100)}}); !errors.Is(err, mydi.ErrNoPort) {
		t.Fatalf("%v", err)
	}
	f.mu.Lock()
	f.fail = false
	f.written = nil
	f.mu.Unlock()
	if err := h.Send("owner-a", "synth", []mydi.Event{{Type: "noteOn", Channel: 5, Note: intp(62), Velocity: intp(100)}}); err != nil {
		t.Fatal(err)
	}
	if got := f.messages(); len(got) < 2 || !bytes.Equal(got[0], []byte{0xB4, 123, 0}) {
		t.Fatalf("% X", got)
	}
}

func TestNoPulseIsWrittenAfterTheClocksStopIsAskedFor(t *testing.T) {
	// ADR-0007: pulses are handed to the port's writer 2 ms ahead of their time; a Stop asked for while one waits there takes it back, so
	// a receiver never counts a pulse after the Stop the listener asked for. Each Stop is asked 1 ms before the clock's third pulse is due,
	// while it waits handed; the pulse before it was due 20 ms earlier, so a pulse written after the Stop has returned can only be the one
	// handed. The time is read when the call returns, not before it: on a loaded machine the test can be paused between reading the clock
	// and calling, and the pulse is then rightly written on its time before the Stop reaches the hub
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	interval := int64(time.Minute) / (120 * 24)
	late := 0
	for i := 0; i < 20; i++ {
		started := mydi.Now()
		if err := h.Clock("synth", 120, true); err != nil {
			t.Fatal(err)
		}
		if wait := time.Duration(started + 3*interval - int64(time.Millisecond) - mydi.Now()); wait > 0 {
			time.Sleep(wait)
		}
		if err := h.Clock("synth", 0, false); err != nil {
			t.Fatal(err)
		}
		asked := mydi.Now()
		f.mu.Lock()
		for j := len(f.written) - 1; j >= 0 && f.times[j] > asked; j-- {
			if f.written[j][0] == 0xF8 {
				late++
			}
		}
		f.mu.Unlock()
	}
	if late > 0 {
		t.Fatalf("%d pulses written after their clock's Stop was asked for", late)
	}
}

func TestTheClockAListenedInputBringsInIsCountedBetweenStartAndStop(t *testing.T) {
	// a device that re-sends the clock it receives brings the hub's own back on the port the program listens to: its pulses between
	// Start and Stop are counted, those around them (the device's own clock while stopped) are not, and none reaches a listener
	var heard []mydi.Received
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), func(r mydi.Received) { heard = append(heard, r) }, nil)
	defer h.Close()
	h.Listen()
	f.mu.Lock()
	deliver := f.deliver
	f.mu.Unlock()
	for _, m := range [][]byte{{0xF8}, {0xF8}, {0xFA}, {0xF8}, {0xF8}, {0xF8}, {0xF8}, {0xF8}, {0xFC}, {0xF8}, {0xF8}, {0xF8}} {
		deliver(m)
	}
	var got mydi.PortHeard
	for _, p := range h.Timing().Heard {
		if p.ID == "synth" {
			got = p
		}
	}
	if got.Pulses != 5 || got.Starts != 1 || got.Stops != 1 {
		t.Fatalf("heard %+v", got)
	}
	for _, r := range heard {
		if r.Event.Type == "" {
			t.Fatalf("a clock pulse reached a listener: %+v", r)
		}
	}
}

func TestTheClockKeepsTimeAndStops(t *testing.T) {
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 600, true); err == nil {
		t.Fatal("took 600 bpm")
	}
	if err := h.Clock("synth", 300, true); err != nil { // 120 pulses a second
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	_ = h.Clock("synth", 0, false)
	pulses := 0
	for _, m := range f.messages() {
		if bytes.Equal(m, []byte{0xF8}) {
			pulses++
		}
	}
	got := f.messages()
	if !bytes.Equal(got[0], []byte{0xFA}) || !bytes.Equal(got[len(got)-1], []byte{0xFC}) || pulses < 45 || pulses > 70 {
		t.Fatalf("%d pulses in half a second at 300 bpm, % X ... % X", pulses, got[0], got[len(got)-1])
	}
	// Stop is the last word: no pulse follows it, and the time thread counted what it wrote
	time.Sleep(50 * time.Millisecond)
	if after := f.messages(); len(after) != len(got) {
		t.Fatalf("%d more after Stop", len(after)-len(got))
	}
	if timing := h.Timing(); timing.Pulses != uint64(pulses) || timing.Priority == "" {
		t.Fatalf("%+v for %d pulses", timing, pulses)
	}
}

func TestTimedEventsPlayInTheirTimeOrderAndThoseOfOneTimeInTheirOrder(t *testing.T) {
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	_ = h.Send("owner-a", "synth", []mydi.Event{
		{Type: "noteOn", Channel: 1, Note: intp(62), Velocity: intp(1), At: 60},
		{Type: "noteOn", Channel: 1, Note: intp(60), Velocity: intp(1), At: 30},
		{Type: "noteOn", Channel: 1, Note: intp(61), Velocity: intp(1), At: 30},
	})
	deadline := time.Now().Add(2 * time.Second)
	for len(f.messages()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got := f.messages()
	if len(got) != 3 || got[0][1] != 60 || got[1][1] != 61 || got[2][1] != 62 {
		t.Fatalf("% X", got)
	}
}

func TestWhatComesInIsHandedOnWithItsTimeAndWithoutTheClock(t *testing.T) {
	f := &fake{}
	var mu sync.Mutex
	var got []mydi.Received
	h := mydi.NewHub(f, only("synth"), func(r mydi.Received) { mu.Lock(); got = append(got, r); mu.Unlock() }, nil)
	defer h.Close()
	h.Listen()
	f.mu.Lock()
	deliver := f.deliver
	f.mu.Unlock()
	deliver([]byte{0xF8, 0xFE, 0x90, 60, 100, 0xF8})
	deliver([]byte{0xB0, 1, 64})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Event.Type != "noteOn" || got[1].Event.Type != "cc" || got[0].Port != "synth" || got[0].At.IsZero() {
		t.Fatalf("%+v", got)
	}
}

func TestIDsAreWhatAURLTakes(t *testing.T) {
	for name, id := range map[string]string{"Microsoft GS Wavetable Synth": "microsoft-gs-wavetable-synth", "MPKmini2 0": "mpkmini2-0", "hw:1,0": "hw-1-0", "***": "port"} {
		if got := mydi.IDOf(name); got != id {
			t.Fatalf("%q: %q", name, got)
		}
	}
}

func TestAClockWhosePortIsLostStopsTryingAndStartsAgainWhenAskedOnceItIsBack(t *testing.T) {
	// F11: the port unplugged under a running clock: the failing pulse ends the clock and closes the port, so the time thread does not
	// try it again pulse after pulse; once the port is back, the clock asked again opens it and starts with Start
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 300, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.messages()) > 4 })
	f.mu.Lock()
	f.fail, f.attempts = true, 0
	f.mu.Unlock()
	time.Sleep(500 * time.Millisecond) // a check that something does not happen: a pulse every 8 ms would be 60 tries
	f.mu.Lock()
	tries := f.attempts
	f.fail, f.written = false, nil
	f.mu.Unlock()
	if tries > 1 {
		t.Fatalf("the lost port was tried %d times", tries)
	}
	if err := h.Clock("synth", 300, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.messages()) > 2 })
	if got := f.messages(); !bytes.Equal(got[0], []byte{0xFA}) {
		t.Fatalf("% X", got)
	}
	_ = h.Clock("synth", 0, false)
}

// waitFor waits for a condition, with a generous bound: the suites run beside other work.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); !ok(); {
		if time.Now().After(end) {
			t.Fatal("the condition never held")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAWriteTheDriverHoldsIsReportedWhileItLasts(t *testing.T) {
	// F9: the driver's write does not return (a USB driver hung): the time thread can do nothing about it, but the timing says so while
	// it lasts, and once it returns, how long it took
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 300, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.messages()) > 4 })
	release := make(chan struct{})
	f.mu.Lock()
	f.block = release
	f.mu.Unlock()
	waitFor(t, func() bool { return h.Timing().BlockedMs > 100 })
	f.mu.Lock()
	f.block = nil
	f.mu.Unlock()
	close(release)
	waitFor(t, func() bool { return h.Timing().BlockedMs == 0 })
	if tm := h.Timing(); tm.SlowWrites == 0 || tm.LongestWriteMs < 100 {
		t.Fatalf("%+v", tm)
	}
	_ = h.Clock("synth", 0, false)
}

// playInto plays a source's song into the stand-in's stamped input in real time: each message stamped by the system at the start of the
// millisecond it came in, and by the callback as it is called.
func (f *fake) playInto(song []heard, start int64) {
	for _, h := range song {
		at := start + h.at
		if wait := time.Duration(at - mydi.Now()); wait > 0 {
			time.Sleep(wait)
		}
		f.mu.Lock()
		deliver := f.stamped
		f.mu.Unlock()
		deliver(mydi.Stamped{Message: h.m, Driver: at - at%ms, Received: mydi.Now()})
	}
}

func TestTheHubFollowsAPortsClockAndSendsItOnWithItsCountKept(t *testing.T) {
	// step 3's wiring on stand-in ports: a source plays Start, four beats at 240 BPM and Stop into one port; the hub follows it with the
	// echo test on and sends the clock on to another, written by the time thread at its time. MYDI_FOLLOW_BEATS makes the song longer,
	// and the clock measure's MYDI_TIMING_LOAD and _MBPS load the collector meanwhile; MYDI_FOLLOW_MEASURE=1 makes it a measure
	// of this machine that reports and does not judge (ADR-0004 step 3), for a run under a load it asks for or one made beside it. Under
	// load it measures the stand-in as much as the hub: the stand-in's source is a goroutine of this process, starved with the rest,
	// where a device's pulses are stamped by the system and only their delivery is held
	measure := os.Getenv("MYDI_FOLLOW_MEASURE") == "1"
	beats := 4
	if n, err := strconv.Atoi(os.Getenv("MYDI_FOLLOW_BEATS")); err == nil && n > 0 {
		beats = n
	}
	var done atomic.Bool
	defer done.Store(true)
	allocating(&done)
	f := &fake{}
	h := mydi.NewHub(f, only("synth", "daw"), nil, nil)
	defer h.Close()
	echo := mydi.DefaultEcho
	following, err := h.Follow("daw", []string{"synth"}, mydi.RemakeConfig{Echo: &echo}, mydi.DefaultFollow)
	if err != nil {
		t.Fatal(err)
	}
	times := newSource().play(24*beats, 240, nil).times
	base := times[0] - int64(time.Second)
	for i := range times {
		times[i] -= base
	}
	f.playInto(transport(times), mydi.Now()+int64(50*time.Millisecond))
	waitFor(t, func() bool {
		for _, m := range f.messages() {
			if m[0] == 0xFC {
				return true
			}
		}
		return false
	})
	following.Stop()
	var started, pulses int
	for _, m := range f.messages() {
		switch m[0] {
		case 0xFA:
			started++
		case 0xF8:
			if started == 0 {
				t.Fatal("a pulse before Start")
			}
			pulses++
		}
	}
	report := following.Report()
	if !measure && (started != 1 || pulses != len(times) || report.Compared != len(times) || report.State != mydi.FollowLocked) {
		t.Fatalf("Start %d, %d pulses sent for %d, %+v", started, pulses, len(times), report)
	}
	t.Logf("Start %d, %d pulses sent for %d, state %s, stops on loss %d, added %d; %d compared at %.1f BPM; from the second beat: phase median %.2f ms, p99 %.2f, largest %.2f; spacing RMS in %.2f ms, out %.2f; "+
		"delivery lag median %.2f ms, p99 %.2f; the time thread's longest write %.2f ms", started, pulses, len(times), report.State,
		report.Remaker.StopsOnLoss, report.Remaker.Added, report.Compared, report.Tempo,
		report.Settled.Phase.Median, report.Settled.Phase.P99, report.Settled.Phase.Max, report.Settled.SourceRMS, report.Settled.SentRMS,
		report.DeliveryLag.Median, report.DeliveryLag.P99, h.Timing().LongestWriteMs)
}

func TestAPortFollowedIsNeverClocked(t *testing.T) {
	f := &fake{}
	h := mydi.NewHub(f, only("synth"), nil, nil)
	defer h.Close()
	if _, err := h.Follow("synth", []string{"synth"}, mydi.RemakeConfig{}, mydi.DefaultFollow); !errors.Is(err, mydi.ErrFollowSamePort) {
		t.Fatalf("%v", err)
	}
}

func TestAFollowedPortUnpluggedAndPluggedBackIsFollowedAgain(t *testing.T) {
	// case F15 on a real synth (2026-11-15): its cable pulled and put back, the follower never heard it again, its input closed with
	// the device and never opened again. Now the hub's look at the ports (Listen, or Revive where it does not listen) closes a followed
	// input whose port is gone and opens it again when the port is back, and the generic listener never takes the port meanwhile
	f := &fake{missing: map[string]bool{}}
	h := mydi.NewHub(f, only("synth", "daw"), nil, nil)
	defer h.Close()
	following, err := h.Follow("daw", []string{"synth"}, mydi.RemakeConfig{}, mydi.DefaultFollow)
	if err != nil {
		t.Fatal(err)
	}
	play := func() {
		times := newSource().play(24*2, 240, nil).times
		base := times[0] - int64(time.Second)
		for i := range times {
			times[i] -= base
		}
		f.playInto(transport(times), mydi.Now()+int64(20*time.Millisecond))
	}
	starts := func() int {
		n := 0
		for _, m := range f.messages() {
			if m[0] == 0xFA {
				n++
			}
		}
		return n
	}
	play()
	waitFor(t, func() bool { return starts() == 1 })
	f.mu.Lock()
	f.missing["daw"] = true
	f.mu.Unlock()
	h.Listen()
	h.Revive()
	f.mu.Lock()
	f.missing["daw"] = false
	f.mu.Unlock()
	h.Listen()
	f.mu.Lock()
	opened, stamped := append([]string(nil), f.openedIn...), append([]string(nil), f.stampedIn...)
	f.mu.Unlock()
	for _, id := range opened {
		if id == "daw" {
			t.Fatalf("the generic listener opened the followed port: %v", opened)
		}
	}
	if len(stamped) != 2 || following.Report().Returns != 1 {
		t.Fatalf("the followed input opened %v, returns %d", stamped, following.Report().Returns)
	}
	play()
	waitFor(t, func() bool { return starts() == 2 })
	following.Stop()
	t.Logf("the port gone and back: its input opened again (%v), the next Start followed", stamped)
}
