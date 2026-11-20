package mydi_test

import (
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// An event the echo test sees: a pulse the hub wrote, or one it heard.
type timed struct {
	at    int64
	wrote bool
}

// judge plays the events in their time order and answers the verdict on each pulse heard, in order.
func judge(e *mydi.Echo, events []timed) []string {
	sort.SliceStable(events, func(i, j int) bool { return events[i].at < events[j].at })
	var verdicts []string
	for _, ev := range events {
		if ev.wrote {
			e.Wrote(ev.at)
		} else {
			verdicts = append(verdicts, e.Judge(ev.at))
		}
	}
	return verdicts
}

func count(verdicts []string, of string) (n int) {
	for _, v := range verdicts {
		if v == of {
			n++
		}
	}
	return n
}

// ownWrites is the hub's own clock as written: on its grid, each write off it by the scheduler's own irregularity, and the dither when
// asked for.
func ownWrites(r *rand.Rand, n int, irregular float64, dither time.Duration) []int64 {
	out := make([]int64, n)
	for k := range out {
		out[k] = int64(float64(time.Second)+float64(k)*interval(120)+r.NormFloat64()*irregular) + mydi.Dither(uint64(k), dither)
	}
	return out
}

func echoOf(r *rand.Rand, writes []int64, delay time.Duration, stamp float64, round time.Duration) []timed {
	var out []timed
	for _, w := range writes {
		at := w + int64(delay) + int64(r.NormFloat64()*stamp)
		if round > 0 {
			at -= at % int64(round)
		}
		out = append(out, timed{at: at})
	}
	return out
}

func written(writes []int64) []timed {
	out := make([]timed, len(writes))
	for i, w := range writes {
		out[i] = timed{at: w, wrote: true}
	}
	return out
}

func TestASoftThruEchoOfTheSpursOwnClockIsKnownByItsFingerprint(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	writes := ownWrites(r, 200, 0.2e6, 0)
	e := mydi.NewEcho(mydi.DefaultEcho)
	verdicts := judge(e, append(written(writes), echoOf(r, writes, 3*time.Millisecond, 0.02e6, 0)...))
	// decided within the window, and every pulse after it refused
	firstEcho := -1
	for i, v := range verdicts {
		if v == mydi.EchoYes {
			firstEcho = i
			break
		}
	}
	delay, ok := e.Delay()
	if firstEcho < 0 || firstEcho > 2*mydi.DefaultEcho.Window || count(verdicts[firstEcho:], mydi.EchoYes) != len(verdicts)-firstEcho {
		t.Fatalf("first echo at %d of %d, verdicts %v", firstEcho, len(verdicts), verdicts[:40])
	}
	if !ok || delay < 2*time.Millisecond || delay > 4*time.Millisecond || count(verdicts[:firstEcho], mydi.EchoNone) != 0 {
		t.Fatalf("delay %v, %d taken as real before", delay, count(verdicts[:firstEcho], mydi.EchoNone))
	}
	t.Logf("echo known at pulse %d, delay %v, correlation %.2f", firstEcho+1, delay, e.Correlation())
}

func TestARealMasterTheSpurFollowsIsNeverTakenForAnEcho(t *testing.T) {
	// the negative control: the hub phase-locked to a master, writing each pulse just after the master's, so the master's pulses sit at
	// a near fixed delay after the hub's previous ones; their irregularities are the master's own
	r := rand.New(rand.NewSource(6))
	var events []timed
	for k := 0; k < 400; k++ {
		master := float64(time.Second) + float64(k)*interval(120) + r.NormFloat64()*0.5e6
		events = append(events, timed{at: int64(master)}, timed{at: int64(master + 0.4e6 + r.NormFloat64()*0.2e6), wrote: true})
	}
	e := mydi.NewEcho(mydi.DefaultEcho)
	verdicts := judge(e, events)
	if n := count(verdicts, mydi.EchoYes); n != 0 {
		t.Fatalf("%d of the master's pulses taken for echoes (correlation %.2f)", n, e.Correlation())
	}
	// a chance pair at a new delay, with no evidence yet, is held a moment as suspect: rare, and never refused
	// unknown pulses (no evidence yet) are left to the follower's line, which learns those on it; suspects are held
	suspect := count(verdicts[2*mydi.DefaultEcho.Window:], mydi.EchoSuspect)
	if suspect*100 > len(verdicts) {
		t.Fatalf("suspect after the window: %d of %d", suspect, len(verdicts))
	}
	t.Logf("a master's pulses: none refused, %d of %d held a moment as suspect", suspect, len(verdicts))
}

func TestAChainLoopedRoundAfterLockIsRefusedPulseByPulse(t *testing.T) {
	// following a master, then a DIN chain closed so the hub's own pulses come back 4 ms later among the master's: those are refused,
	// the master's are not
	r := rand.New(rand.NewSource(8))
	var events []timed
	var echoes int
	for k := 0; k < 400; k++ {
		master := float64(time.Second) + float64(k)*interval(120) + r.NormFloat64()*0.5e6
		wrote := master + 0.4e6 + r.NormFloat64()*0.2e6
		events = append(events, timed{at: int64(master)}, timed{at: int64(wrote), wrote: true})
		if k >= 150 {
			events = append(events, timed{at: int64(wrote + 4e6 + r.NormFloat64()*0.02e6)})
			echoes++
		}
	}
	e := mydi.NewEcho(mydi.DefaultEcho)
	verdicts := judge(e, events)
	refused := count(verdicts, mydi.EchoYes)
	// every echo after the window that decides it, and no master pulse
	if refused < echoes-2*mydi.DefaultEcho.Window || refused > echoes {
		t.Fatalf("%d refused of %d echoes", refused, echoes)
	}
	if delay, ok := e.Delay(); !ok || delay < 3*time.Millisecond || delay > 5*time.Millisecond {
		t.Fatalf("delay %v", delay)
	}
}

func TestUSBsMillisecondFramesBlurTheFingerprintAndTheDitherRestoresIt(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	// the natural fingerprint, input rounded to the millisecond: too faint to know
	writes := ownWrites(r, 300, 0.2e6, 0)
	plain := mydi.NewEcho(mydi.DefaultEcho)
	natural := count(judge(plain, append(written(writes), echoOf(r, writes, 3*time.Millisecond, 0.02e6, time.Millisecond)...)), mydi.EchoYes)
	// the dither, half a millisecond either way: known through the rounding
	dithered := ownWrites(r, 300, 0.2e6, 500*time.Microsecond)
	withDither := mydi.NewEcho(mydi.DefaultEcho)
	known := count(judge(withDither, append(written(dithered), echoOf(r, dithered, 3*time.Millisecond, 0.02e6, time.Millisecond)...)), mydi.EchoYes)
	if known < 300-2*mydi.DefaultEcho.Window {
		t.Fatalf("with the dither, %d of 300 known (correlation %.2f)", known, withDither.Correlation())
	}
	t.Logf("rounded to 1 ms: natural fingerprint %d of 300 known (correlation %.2f), dithered %d (%.2f)",
		natural, plain.Correlation(), known, withDither.Correlation())
}

func TestTheEchoTestHoldsOverManySeeds(t *testing.T) {
	refused, suspects, judged, missed, leaked, chainShort := 0, 0, 0, 0, 0, 0
	for seed := int64(1); seed <= seeds(); seed++ {
		r := rand.New(rand.NewSource(seed))
		// a real master the hub follows
		var events []timed
		for k := 0; k < 400; k++ {
			master := float64(time.Second) + float64(k)*interval(120) + r.NormFloat64()*0.5e6
			events = append(events, timed{at: int64(master)}, timed{at: int64(master + 0.4e6 + r.NormFloat64()*0.2e6), wrote: true})
		}
		v := judge(mydi.NewEcho(mydi.DefaultEcho), events)
		refused += count(v, mydi.EchoYes)
		suspects += count(v[2*mydi.DefaultEcho.Window:], mydi.EchoSuspect)
		judged += len(v) - 2*mydi.DefaultEcho.Window
		// a soft-thru echo of the hub's own clock: known within two windows, none taken as real before
		writes := ownWrites(r, 200, 0.2e6, 0)
		w := judge(mydi.NewEcho(mydi.DefaultEcho), append(written(writes), echoOf(r, writes, 3*time.Millisecond, 0.02e6, 0)...))
		first := -1
		for i, x := range w {
			if x == mydi.EchoYes {
				first = i
				break
			}
		}
		switch {
		case first < 0 || first > 2*mydi.DefaultEcho.Window:
			missed++
		case count(w[:first], mydi.EchoNone) > 0:
			leaked++
		}
		// a chain closed after lock: its echoes refused, none of the master's
		var mixed []timed
		echoes := 0
		for k := 0; k < 400; k++ {
			master := float64(time.Second) + float64(k)*interval(120) + r.NormFloat64()*0.5e6
			wrote := master + 0.4e6 + r.NormFloat64()*0.2e6
			mixed = append(mixed, timed{at: int64(master)}, timed{at: int64(wrote), wrote: true})
			if k >= 150 {
				mixed = append(mixed, timed{at: int64(wrote + 4e6 + r.NormFloat64()*0.02e6)})
				echoes++
			}
		}
		if n := count(judge(mydi.NewEcho(mydi.DefaultEcho), mixed), mydi.EchoYes); n < echoes-2*mydi.DefaultEcho.Window || n > echoes {
			chainShort++
		}
	}
	if refused != 0 || missed != 0 || leaked != 0 || chainShort != 0 || suspects*100 > judged {
		t.Fatalf("over %d seeds: master pulses refused %d, suspect %d of %d; echoes missed %d, leaked %d; chains short %d",
			seeds(), refused, suspects, judged, missed, leaked, chainShort)
	}
	t.Logf("over %d seeds: no master pulse refused, %d of %d held a moment as suspect; every echo known, none leaked", seeds(), suspects, judged)
}
