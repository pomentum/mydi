package src

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// The loopback (ADR-0004 part G, step 1): the hub's own clock pulses sent out of one port at known times and read back on another,
// wired to it (a cable from the interface's out to its in, or a device's soft thru), with each pulse's two stamps: the system's own
// (WinMM's) and the one the hub's callback takes. It answers where the system stamps input: on arrival from the device, so its stamps
// keep the pulses' spacing however late the hub reads them, or on delivery to the process, so its stamps bunch with the reads. The
// callback is held now and then on purpose, which makes the system queue what arrives meanwhile and deliver it in a burst: the stamps of
// a burst say which. The follower fits on the system's stamps only if they are taken on arrival (part B). A device with no soft thru is
// listened to instead: its own clock read with the callback held the same way, each burst judged against the source's own spacing.

// LoopbackConfig is one loopback run.
type LoopbackConfig struct {
	// Pulses sent, Interval apart (480 at 20.83 ms: twenty beats at 120 BPM).
	Pulses   int
	Interval time.Duration
	// The callback is held for Hold on every HoldEvery-th pulse read (0: never), so the system queues what arrives meanwhile: long enough
	// for several pulses, or none comes in a burst (a 30 ms hold queued one pulse of a real clock at 120 BPM, and said nothing).
	HoldEvery int
	Hold      time.Duration
	// Settle is how long the stragglers are waited for after the last pulse.
	Settle time.Duration
	// The writes are held for WriteHold on every WriteHoldEvery-th pulse sent (0: never), and the pulses owed then go out CatchUp apart
	// until they are back on their grid: what a port's writer does after its driver held a write (ADR-0006 part C), so a loop shows
	// whether the device and USB pass such a burst whole.
	WriteHoldEvery int
	WriteHold      time.Duration
	CatchUp        time.Duration
}

// DefaultLoopback is twenty beats at 120 BPM, the callback held for 100 ms (about five pulses) every 24 pulses.
var DefaultLoopback = LoopbackConfig{Pulses: 480, Interval: 20833 * time.Microsecond, HoldEvery: 24, Hold: 100 * time.Millisecond, Settle: 500 * time.Millisecond}

// LoopbackPulse is one pulse's times on the timeline: when it was written, the system's stamp on its return (-1: none), and when the
// callback was entered.
type LoopbackPulse struct {
	Sent, Driver, Received int64
}

// LoopDriver is a driver that can be looped: it writes, and reads with stamps.
type LoopDriver interface {
	Driver
	StampedDriver
}

// RunLoopback sends the pulses out of one port and reads them on the other, paired in order; a pulse lost or added is an error, since
// the pairs would no longer say anything.
func RunLoopback(d LoopDriver, out, in string, c LoopbackConfig) ([]LoopbackPulse, error) {
	if c.Pulses <= 0 || c.Interval <= 0 {
		return nil, errors.New("a loopback sends at least one pulse, some time apart")
	}
	var mu sync.Mutex
	var read []Stamped
	input, err := d.OpenInStamped(in, func(m Stamped) {
		if len(m.Message) != 1 || m.Message[0] != 0xF8 {
			return // only the pulses: a device's active sensing and the rest are not ours
		}
		mu.Lock()
		read = append(read, m)
		n := len(read)
		mu.Unlock()
		if c.HoldEvery > 0 && n%c.HoldEvery == 0 {
			time.Sleep(c.Hold) // the callback held, as Go's pauses or the system hold it
		}
	})
	if err != nil {
		return nil, fmt.Errorf("the input %s: %w", in, err)
	}
	defer input.Close()
	output, err := d.OpenOut(out)
	if err != nil {
		return nil, fmt.Errorf("the output %s: %w", out, err)
	}
	defer output.Close()
	sent := make([]int64, 0, c.Pulses)
	began := make([]int64, 0, c.Pulses) // when each write call began
	start := Now() + int64(50*time.Millisecond)
	// free is when the writes may go on after a hold; last when the last pulse was due to go out
	free, last := int64(0), int64(0)
	for i := 0; i < c.Pulses; i++ {
		at := start + int64(i)*int64(c.Interval)
		if c.WriteHoldEvery > 0 && i > 0 && i%c.WriteHoldEvery == 0 {
			free = at + int64(c.WriteHold)
		}
		// on its grid, never before a hold ends, never closer than CatchUp to the last: the owed pulses after a hold go out CatchUp apart
		// until the grid is caught up with
		at = max(at, free)
		if c.WriteHoldEvery > 0 && last > 0 {
			at = max(at, last+int64(c.CatchUp))
		}
		if wait := time.Duration(at - Now()); wait > 0 {
			time.Sleep(wait)
		}
		before := Now()
		if err := output.Write([]byte{0xF8}); err != nil {
			return nil, fmt.Errorf("writing a pulse: %w", err)
		}
		sent = append(sent, before+(Now()-before)/2)
		began = append(began, before)
		last = at
	}
	time.Sleep(c.Settle)
	mu.Lock()
	defer mu.Unlock()
	// only what came back while the pulses went out, and a moment after: a device with its own clock sends that again once the loop's
	// stops (a synth whose clock send is on, 2026-11-16)
	kept := read[:0]
	for _, r := range read {
		if r.Received >= sent[0] && r.Received <= sent[len(sent)-1]+int64(30*time.Millisecond) {
			kept = append(kept, r)
		}
	}
	read = kept
	if len(read) == len(sent) {
		pulses := make([]LoopbackPulse, len(sent))
		for i := range sent {
			pulses[i] = LoopbackPulse{Sent: sent[i], Driver: read[i].Driver, Received: read[i].Received}
		}
		return pulses, nil
	}
	// a count that differs: each pulse sent paired, in order, with the first unpaired one that came back from when its write call began
	// to 15 ms after: never against the middle of the call, since the first write after a pause can take its call 18 ms and its echo
	// comes back within it (2026-11-16); one sent with none back is lost, one back with none sent the device's own
	var pulses []LoopbackPulse
	next := 0
	lost := 0
	for i, s := range sent {
		for next < len(read) && read[next].Received < began[i] {
			next++
		}
		if next < len(read) && read[next].Received <= began[i]+int64(15*time.Millisecond) {
			pulses = append(pulses, LoopbackPulse{Sent: s, Driver: read[next].Driver, Received: read[next].Received})
			next++
			continue
		}
		lost++
	}
	readAt := make([]int64, len(read))
	for i, r := range read {
		readAt[i] = r.Received
	}
	return pulses, &LoopCount{Sent: len(sent), Read: len(read), Lost: lost, Foreign: len(read) - len(pulses), SentAt: sent, ReadAt: readAt}
}

// LoopCount is a loop whose count differed: the pulses sent and read, those sent that never came back, and those back that no pulse
// sent explains (a device's own).
type LoopCount struct {
	Sent, Read, Lost, Foreign int
	// SentAt and ReadAt are every pulse sent and read, on the timeline, to see what the device did
	SentAt, ReadAt []int64
}

func (c *LoopCount) Error() string {
	return fmt.Sprintf("%d pulses sent and %d read: %d sent never came back, %d came back that none sent explains (the device's own)",
		c.Sent, c.Read, c.Lost, c.Foreign)
}

// Listen reads a device's own clock on one port, with nothing sent (step 1 for a device with no soft thru): the callback is held as in a
// loopback, and a burst's stamps are judged against the source's own spacing, for want of the times its pulses were sent. Its pulses carry
// Sent -1. It reads c.Pulses pulses, or what comes within the time given, whichever is first.
func Listen(d StampedDriver, in string, c LoopbackConfig, within time.Duration) ([]LoopbackPulse, error) {
	if c.Pulses <= 0 || within <= 0 {
		return nil, errors.New("a listen reads at least one pulse, for some time")
	}
	var mu sync.Mutex
	var read []Stamped
	full := make(chan struct{})
	input, err := d.OpenInStamped(in, func(m Stamped) {
		if len(m.Message) != 1 || m.Message[0] != 0xF8 {
			return
		}
		mu.Lock()
		read = append(read, m)
		n := len(read)
		mu.Unlock()
		if n == c.Pulses {
			close(full)
		}
		if c.HoldEvery > 0 && n%c.HoldEvery == 0 {
			time.Sleep(c.Hold)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("the input %s: %w", in, err)
	}
	select {
	case <-full:
	case <-time.After(within):
	}
	_ = input.Close()
	mu.Lock()
	defer mu.Unlock()
	if least := max(48, 3*c.HoldEvery); len(read) < least {
		return nil, fmt.Errorf("%d pulses heard in %s, %d at least are needed: is the device sending its clock (its clock send on, and playing if it sends only then)?",
			len(read), within, least)
	}
	n := min(len(read), c.Pulses)
	pulses := make([]LoopbackPulse, n)
	for i := range pulses {
		pulses[i] = LoopbackPulse{Sent: -1, Driver: read[i].Driver, Received: read[i].Received}
	}
	return pulses, nil
}

// sourceStep is a listened source's own spacing: its stamps' span over the pulses, or the reads' where the system gives no stamp, which
// stamps rounded to the millisecond do not bias; the median of the gaps where the span is not near it (the source stopped a while). A
// burst bunches a few gaps in each held stretch, too few to move the median.
func sourceStep(pulses []LoopbackPulse) int64 {
	median := medianGap(pulses)
	first, last := pulses[0], pulses[len(pulses)-1]
	span := last.Received - first.Received
	if first.Driver >= 0 && last.Driver >= 0 {
		span = last.Driver - first.Driver
	}
	if mean := span / int64(len(pulses)-1); math.Abs(float64(mean-median)) <= float64(median)/10 {
		return mean
	}
	return median
}

func medianGap(pulses []LoopbackPulse) int64 {
	var gaps []float64
	for i := 1; i < len(pulses); i++ {
		if pulses[i].Driver >= 0 && pulses[i-1].Driver >= 0 {
			gaps = append(gaps, float64(pulses[i].Driver-pulses[i-1].Driver))
		} else {
			gaps = append(gaps, float64(pulses[i].Received-pulses[i-1].Received))
		}
	}
	return int64(spreadOf(gaps).Median)
}

// Spread is a set of times in milliseconds: its median, 99th percentile and largest.
type Spread struct {
	Median, P99, Max float64
}

func spreadOf(values []float64) Spread {
	if len(values) == 0 {
		return Spread{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	at := func(q float64) float64 {
		return sorted[int(math.Min(float64(len(sorted)-1), math.Floor(q*float64(len(sorted)))))]
	}
	return Spread{Median: at(0.5), P99: at(0.99), Max: sorted[len(sorted)-1]}
}

// What a loopback says of the system's stamps.
const (
	StampsOnArrival  = "on arrival"
	StampsOnDelivery = "on delivery"
	StampsUnclear    = "unclear"
	StampsNone       = "none"
)

// LoopbackReport is what a run shows.
type LoopbackReport struct {
	Pulses int `json:"pulses"`
	// Listened is a device's own clock read with nothing sent, Step its spacing in milliseconds: there is no loop's delay to say.
	Listened bool    `json:"listened,omitempty"`
	Step     float64 `json:"stepMs,omitempty"`
	// Latency is the system's stamp less the time sent: the loop's own delay as the system sees it.
	Latency Spread `json:"latencyMs"`
	// CallbackLag is the callback's stamp less the system's: how late the hub read what the system had stamped.
	CallbackLag Spread `json:"callbackLagMs"`
	// Burst is how many pulses were read in a burst (read under a quarter interval after the one before, though sent a whole interval
	// after it); Kept, how many of those the system stamped a whole interval apart within 2 ms; Bunched, how many it stamped under a
	// quarter interval apart.
	Burst   int `json:"burst"`
	Kept    int `json:"kept"`
	Bunched int `json:"bunched"`
	// Stamps is the verdict: on arrival, on delivery, unclear, or none (the system gives no stamp).
	Stamps string `json:"stamps"`
	// Spacing is how far the system's stamps' spacing is from the spacing the pulses were sent with, every pulse: what a follower fitting
	// on them would see of the loop.
	Spacing Spread `json:"spacingMs"`
}

// AnalyseLoopback reads a run: a burst whose system stamps keep the sent spacing says the system stamps on arrival; one whose stamps
// bunch with the reads says on delivery. At least three pulses must have come in bursts, and nine in ten of them agree, for a verdict.
func AnalyseLoopback(pulses []LoopbackPulse, interval time.Duration) LoopbackReport {
	r := LoopbackReport{Pulses: len(pulses), Stamps: StampsUnclear}
	if len(pulses) < 2 {
		return r
	}
	if pulses[0].Driver < 0 {
		r.Stamps = StampsNone
	}
	ms := func(ns int64) float64 { return float64(ns) / 1e6 }
	// a listened source is judged against its own spacing, the times it sent its pulses being unknown
	step := int64(interval)
	if r.Listened = pulses[0].Sent < 0; r.Listened {
		step = sourceStep(pulses)
		r.Step = ms(step)
	}
	var latency, lag, spacing []float64
	quarter := step / 4
	for i, p := range pulses {
		if p.Driver >= 0 {
			if !r.Listened {
				latency = append(latency, ms(p.Driver-p.Sent))
			}
			lag = append(lag, ms(p.Received-p.Driver))
		}
		if i == 0 {
			continue
		}
		prev := pulses[i-1]
		sentGap := step
		if !r.Listened {
			sentGap = p.Sent - prev.Sent
		}
		if p.Driver >= 0 {
			spacing = append(spacing, math.Abs(ms((p.Driver-prev.Driver)-sentGap)))
		}
		if p.Received-prev.Received < quarter && sentGap > 3*quarter {
			r.Burst++
			if p.Driver >= 0 {
				switch driverGap := p.Driver - prev.Driver; {
				case math.Abs(ms(driverGap-sentGap)) <= 2:
					r.Kept++
				case driverGap < quarter:
					r.Bunched++
				}
			}
		}
	}
	r.Latency, r.CallbackLag, r.Spacing = spreadOf(latency), spreadOf(lag), spreadOf(spacing)
	if r.Stamps == StampsNone || r.Burst < 3 {
		return r
	}
	switch {
	case r.Kept*10 >= r.Burst*9:
		r.Stamps = StampsOnArrival
	case r.Bunched*10 >= r.Burst*9:
		r.Stamps = StampsOnDelivery
	}
	return r
}

// String is the report for a person.
func (r LoopbackReport) String() string {
	var b strings.Builder
	spacing := "sent"
	if r.Listened {
		spacing = "source's own"
		fmt.Fprintf(&b, "%d pulses of the device's own clock, %.2f ms apart (%.1f BPM)\n", r.Pulses, r.Step, 60000/(24*r.Step))
	} else {
		fmt.Fprintf(&b, "%d pulses through the loop\n", r.Pulses)
	}
	if r.Stamps == StampsNone {
		b.WriteString("this system's MIDI gives no stamp of its own: the follower would fit on the reader's wake-up (ADR-0004 part B)\n")
		return b.String()
	}
	if !r.Listened {
		fmt.Fprintf(&b, "  the loop's delay, the system's stamp less the time sent: median %.2f ms, 99th %.2f ms, largest %.2f ms\n",
			r.Latency.Median, r.Latency.P99, r.Latency.Max)
	}
	fmt.Fprintf(&b, "  the hub's read after the system's stamp: median %.2f ms, 99th %.2f ms, largest %.2f ms\n",
		r.CallbackLag.Median, r.CallbackLag.P99, r.CallbackLag.Max)
	fmt.Fprintf(&b, "  the stamps' spacing off the %s spacing: median %.2f ms, 99th %.2f ms, largest %.2f ms\n",
		spacing, r.Spacing.Median, r.Spacing.P99, r.Spacing.Max)
	fmt.Fprintf(&b, "  read in bursts: %d pulses, of which the system stamped %d at their %s spacing and %d bunched\n",
		r.Burst, r.Kept, spacing, r.Bunched)
	switch r.Stamps {
	case StampsOnArrival:
		b.WriteString("the system stamps on arrival: a follower may fit on its stamps (ADR-0004 part B)\n")
	case StampsOnDelivery:
		b.WriteString("the system stamps on delivery: its stamps bunch with the reads, and a follower needs another stamp (ADR-0004 part B)\n")
	default:
		b.WriteString("unclear: too few pulses came in bursts, or they disagree; run it again, longer\n")
	}
	return b.String()
}
