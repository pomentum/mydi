package mydi_test

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// loop is a stand-in for a MIDI interface whose out is wired to its in, and for the system's MIDI service between them: what is written
// arrives a millisecond later and is delivered to the reader in order, one at a time, so a reader that is held makes what arrives meanwhile
// wait and come in a burst. It stamps each message on its arrival, or, as the other system would, on its delivery.
type loop struct {
	onDelivery bool
	queue      chan int64
	deliver    func(mydi.Stamped)
	done       sync.WaitGroup
}

func newLoop(onDelivery bool) *loop {
	return &loop{onDelivery: onDelivery, queue: make(chan int64, 4096)}
}

func (l *loop) Ports() ([]mydi.PortInfo, error) {
	return []mydi.PortInfo{{ID: "loop", Name: "loop", In: true, Out: true}}, nil
}
func (l *loop) OpenIn(string, func([]byte)) (mydi.Input, error) { return nil, mydi.ErrNoPort }

func (l *loop) OpenInStamped(_ string, deliver func(mydi.Stamped)) (mydi.Input, error) {
	l.deliver = deliver
	l.done.Add(1)
	go func() {
		defer l.done.Done()
		for arrival := range l.queue {
			if wait := time.Duration(arrival - mydi.Now()); wait > 0 {
				time.Sleep(wait)
			}
			stamp := arrival
			if l.onDelivery {
				stamp = mydi.Now()
			}
			deliver(mydi.Stamped{Message: []byte{0xF8}, Driver: stamp, Received: mydi.Now()})
		}
	}()
	return closer(func() {}), nil
}

func (l *loop) OpenOut(string) (mydi.Output, error) { return l, nil }

func (l *loop) Write(message []byte) error {
	l.queue <- mydi.Now() + int64(time.Millisecond)
	return nil
}

func (l *loop) Close() error { return nil }

type closer func()

func (c closer) Close() error { c(); return nil }

var quick = mydi.LoopbackConfig{Pulses: 96, Interval: 5 * time.Millisecond, HoldEvery: 12, Hold: 20 * time.Millisecond, Settle: 300 * time.Millisecond}

func TestALoopbackTellsAStampTakenOnArrivalFromOneTakenOnDelivery(t *testing.T) {
	for _, c := range []struct {
		onDelivery bool
		want       string
	}{{false, mydi.StampsOnArrival}, {true, mydi.StampsOnDelivery}} {
		pulses, err := mydi.RunLoopback(newLoop(c.onDelivery), "loop", "loop", quick)
		if err != nil {
			t.Fatal(err)
		}
		report := mydi.AnalyseLoopback(pulses, quick.Interval)
		if report.Stamps != c.want || report.Burst < 3 {
			t.Fatalf("stamped on delivery %v: %+v", c.onDelivery, report)
		}
		if !strings.Contains(report.String(), c.want) {
			t.Fatalf("%s", report)
		}
	}
}

func TestALoopbackWithNoHoldGivesNoVerdict(t *testing.T) {
	// the negative control: with the reader never held nothing comes in a burst, so nothing can be told, either way
	calm := quick
	calm.HoldEvery = 0
	pulses, err := mydi.RunLoopback(newLoop(true), "loop", "loop", calm)
	if err != nil {
		t.Fatal(err)
	}
	if report := mydi.AnalyseLoopback(pulses, calm.Interval); report.Stamps != mydi.StampsUnclear {
		t.Fatalf("%+v", report)
	}
}

func TestALoopbackSaysSoWhenTheSystemGivesNoStamp(t *testing.T) {
	pulses := []mydi.LoopbackPulse{{Sent: 0, Driver: -1, Received: 1e6}, {Sent: 5e6, Driver: -1, Received: 6e6}}
	if report := mydi.AnalyseLoopback(pulses, 5*time.Millisecond); report.Stamps != mydi.StampsNone {
		t.Fatalf("%+v", report)
	}
}

// play is a device's own clock into the stand-in: a pulse every interval, each arriving at its time exactly, nothing sent by the hub.
func (l *loop) play(n int, interval time.Duration) {
	start := mydi.Now() + int64(20*time.Millisecond)
	for i := 0; i < n; i++ {
		l.queue <- start + int64(i)*int64(interval)
	}
}

func TestListeningToADevicesOwnClockTellsAStampTakenOnArrivalFromOneTakenOnDelivery(t *testing.T) {
	// a device with no soft thru: its own clock, the reader held as in a loopback, each burst judged against the source's own spacing
	for _, c := range []struct {
		onDelivery bool
		want       string
	}{{false, mydi.StampsOnArrival}, {true, mydi.StampsOnDelivery}} {
		source := newLoop(c.onDelivery)
		source.play(quick.Pulses+24, quick.Interval)
		pulses, err := mydi.Listen(source, "loop", quick, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		report := mydi.AnalyseLoopback(pulses, 0)
		if report.Stamps != c.want || report.Burst < 3 || !report.Listened || math.Abs(report.Step-5) > 1 {
			t.Fatalf("stamped on delivery %v: %s, %d in bursts, %d kept, %d bunched", c.onDelivery, report.Stamps, report.Burst, report.Kept, report.Bunched)
		}
		if text := report.String(); !strings.Contains(text, c.want) || strings.Contains(text, "the loop's delay") {
			t.Fatalf("%s", text)
		}
	}
}

func TestListeningToASilentPortSaysWhatToTurnOn(t *testing.T) {
	_, err := mydi.Listen(newLoop(false), "loop", quick, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "clock send") {
		t.Fatalf("%v", err)
	}
}
