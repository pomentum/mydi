package mydi

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// The clock's own measure (ADR-0003): a clock of the hub run on a stand-in port that stamps each pulse as it is written, on the time
// thread's timeline, and on a real port of the machine as well when one is named; how far each pulse lands from its grid, the gaps
// between them, and the tempo a receiver reads beat by beat. `mydi timing` runs it, as a service too (ADR-0003's service
// test), and the clock timing test reports with it.

// ClockMeasure is how a clock kept time, from the moments its pulses were written, in milliseconds and BPM.
type ClockMeasure struct {
	Port   string  `json:"port,omitempty"`
	BPM    float64 `json:"bpm"`
	Pulses int     `json:"pulses"`
	// Off is each pulse's distance from its place on a grid anchored on the first pulse: median, 99th percentile and worst
	OffMedianMs float64 `json:"offMedianMs"`
	OffP99Ms    float64 `json:"offP99Ms"`
	OffWorstMs  float64 `json:"offWorstMs"`
	// Gap is the time between two pulses: shortest, median, 99th percentile, longest, and RMS off the interval
	GapShortestMs float64 `json:"gapShortestMs"`
	GapMedianMs   float64 `json:"gapMedianMs"`
	GapP99Ms      float64 `json:"gapP99Ms"`
	GapLongestMs  float64 `json:"gapLongestMs"`
	GapRMSMs      float64 `json:"gapRmsMs"`
	// Tempo is what a receiver reads over each beat of 24 pulses: least, median, most, and the distance from the clock's own tempo
	// that 99% of the beats are within
	TempoLeast    float64 `json:"tempoLeast"`
	TempoMedian   float64 `json:"tempoMedian"`
	TempoMost     float64 `json:"tempoMost"`
	TempoWithin99 float64 `json:"tempoWithin99"`
	// DriftMs is the last pulse's distance from its place
	DriftMs float64 `json:"driftMs"`
	// Timing is the hub's own counts, its thread's priority and the process's class included
	Timing Timing `json:"timing"`
	// RestartedAt is the first pulse after the clock's restart (MeasureRestart), and WrittenAt every pulse's write, on the time thread's
	// timeline, for a reader that splits the run there: the grid starts again with the restart
	RestartedAt int     `json:"restartedAt,omitempty"`
	WrittenAt   []int64 `json:"writtenAt,omitempty"`
	// written are the moments the pulses were written, for a measure of what came back
	written []int64
}

// MeasureRestart, when At is set, stops the measured clock that far into its run and starts it again Pause later, in the same process:
// whether a clock's own Start brings the late first minutes an hour showed, or only a process's start does (ADR-0003 part D).
var MeasureRestart struct{ At, Pause time.Duration }

// MeasureOf measures a clock at this tempo from the moments its pulses were written, on the time thread's timeline.
func MeasureOf(pulses []int64, bpm float64, timing Timing) ClockMeasure {
	m := ClockMeasure{BPM: bpm, Pulses: len(pulses), Timing: timing}
	if len(pulses) < 2 {
		return m
	}
	ms := func(ns int64) float64 { return float64(ns) / 1e6 }
	interval := int64(float64(time.Minute) / (bpm * 24))
	var off, gaps, tempos []float64
	var squares float64
	for i, at := range pulses {
		late := ms(at - pulses[0] - int64(i)*interval)
		off = append(off, math.Abs(late))
		if i > 0 {
			gap := ms(at - pulses[i-1])
			gaps = append(gaps, gap)
			squares += (gap - ms(interval)) * (gap - ms(interval))
		}
		if i >= 24 {
			tempos = append(tempos, 60000/ms(at-pulses[i-24]))
		}
		m.DriftMs = late
	}
	quantile := func(values []float64, q float64) float64 {
		if len(values) == 0 {
			return 0
		}
		sorted := append([]float64(nil), values...)
		sort.Float64s(sorted)
		return sorted[int(math.Min(float64(len(sorted)-1), q*float64(len(sorted))))]
	}
	m.OffMedianMs, m.OffP99Ms, m.OffWorstMs = quantile(off, 0.5), quantile(off, 0.99), quantile(off, 1)
	m.GapShortestMs, m.GapMedianMs, m.GapP99Ms, m.GapLongestMs = quantile(gaps, 0), quantile(gaps, 0.5), quantile(gaps, 0.99), quantile(gaps, 1)
	m.GapRMSMs = math.Sqrt(squares / float64(len(gaps)))
	if len(tempos) > 0 {
		away := make([]float64, len(tempos))
		for i, v := range tempos {
			away[i] = math.Abs(v - bpm)
		}
		m.TempoLeast, m.TempoMedian, m.TempoMost, m.TempoWithin99 = quantile(tempos, 0), quantile(tempos, 0.5), quantile(tempos, 1), quantile(away, 0.99)
	}
	return m
}

// measurePort is a stand-in port that stamps each pulse once it is written, and writes it to a real port first when it has one, so the
// real port's own cost is counted.
type measurePort struct {
	mu     sync.Mutex
	pulses []int64
	real   Output
}

func (p *measurePort) Ports() ([]PortInfo, error) {
	return []PortInfo{{ID: "measure", Name: "Measure", Out: true}}, nil
}
func (p *measurePort) OpenOut(string) (Output, error)             { return p, nil }
func (p *measurePort) OpenIn(string, func([]byte)) (Input, error) { return nil, ErrNoPort }
func (p *measurePort) Close() error {
	if p.real != nil {
		return p.real.Close()
	}
	return nil
}
func (p *measurePort) Write(m []byte) error {
	if len(m) == 1 && m[0] == 0xF8 && MeasureHold.After > 0 {
		p.mu.Lock()
		n := len(p.pulses) + 1
		p.mu.Unlock()
		if n == MeasureHold.After || (MeasureHold.Every > 0 && n > MeasureHold.After && (n-MeasureHold.After)%MeasureHold.Every == 0) {
			time.Sleep(MeasureHold.Hold) // a driver's hold, made on purpose
		}
	}
	if p.real != nil {
		if err := p.real.Write(m); err != nil {
			return err
		}
	}
	if len(m) == 1 && m[0] == 0xF8 {
		at := Now()
		p.mu.Lock()
		p.pulses = append(p.pulses, at)
		p.mu.Unlock()
	}
	return nil
}

// MeasureHold, when After is set, holds the write of the measured clock's pulse of that number (from 1) for Hold before it reaches the
// port: a driver's hold made on purpose, so what the port's writer does after it (catch up, or resynchronise past a beat, ADR-0006 part
// C) reaches the device, for a person to see what the device makes of it.
var MeasureHold struct {
	After int
	Hold  time.Duration
	// Every, when set, holds again every this many pulses after the first
	Every int
}

// MeasureClock runs a clock of its own hub at this tempo for this long, or until ctx ends, on a stand-in port, and on the machine's port
// of this id as well when one is named, and measures it.
func MeasureClock(ctx context.Context, bpm float64, length time.Duration, id string) (ClockMeasure, error) {
	port := &measurePort{pulses: make([]int64, 0, int(bpm*24/60*(length.Seconds()+5)))}
	if id != "" {
		real, err := (System{}).OpenOut(id)
		if err != nil {
			return ClockMeasure{}, err
		}
		port.real = real
	}
	h := NewHub(port, func(string) bool { return true }, nil, nil)
	defer h.Close()
	if err := h.Clock("measure", bpm, true); err != nil {
		return ClockMeasure{}, err
	}
	done := make(chan struct{})
	if OnHold != nil {
		go watchHolds(h, done)
	}
	wait := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	restarted := 0
	if r := MeasureRestart; r.At > 0 && r.At+r.Pause < length {
		if wait(r.At) {
			_ = h.Clock("measure", 0, false)
			if wait(r.Pause) {
				port.mu.Lock()
				restarted = len(port.pulses)
				port.mu.Unlock()
				if err := h.Clock("measure", bpm, true); err != nil {
					close(done)
					return ClockMeasure{}, err
				}
				wait(length - r.At - r.Pause)
			}
		}
	} else {
		wait(length)
	}
	close(done)
	_ = h.Clock("measure", 0, false)
	port.mu.Lock()
	pulses := append([]int64(nil), port.pulses...)
	port.mu.Unlock()
	m := MeasureOf(pulses, bpm, h.Timing())
	m.Port, m.written = id, pulses
	if restarted > 0 {
		m.RestartedAt, m.WrittenAt = restarted, pulses
	}
	return m, nil
}

// OnHold, when set, is told once for each write a measured clock's port holds past HoldReported, while the driver still holds it: what
// stops a trace recorded in a circular buffer just after a hold, the only way to see which driver held it.
var OnHold func(heldMs float64)

// HoldReported is how long a write must be held for OnHold to hear of it.
const HoldReported = 100 * time.Millisecond

// watchHolds tells OnHold of each hold past HoldReported, looking every 5 ms until done.
func watchHolds(h *Hub, done chan struct{}) {
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	told := false
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		held := h.Timing().BlockedMs
		switch {
		case held > float64(HoldReported/time.Millisecond) && !told:
			told = true
			OnHold(held)
		case held == 0:
			told = false
		}
	}
}

// BackMeasure is a clock measured where it comes back: the hub clocks a device that sends what it receives back out (a soft thru), and
// every pulse that comes back is counted and stamped by the system, so what a device received is measured, not only what the hub wrote.
type BackMeasure struct {
	Back string `json:"back"`
	// Written and Heard are the pulses the hub wrote and those that came back between Start and Stop; Lost their difference; Starts and
	// Stops how many of each came back
	Written int `json:"written"`
	Heard   int `json:"heard"`
	Lost    int `json:"lost"`
	Starts  int `json:"starts"`
	Stops   int `json:"stops"`
	// Measure is the pulses that came back as a receiver had them: their place on a grid of the clock's tempo, their gaps, the tempo read
	// beat by beat, and the last one's distance from its place (a tempo that drifts moves it on)
	Measure ClockMeasure `json:"measure"`
	// DelayMs is how long each pulse took to come back, written to heard, paired in order: median, 99th percentile, most
	DelayMedianMs float64 `json:"delayMedianMs"`
	DelayP99Ms    float64 `json:"delayP99Ms"`
	DelayMostMs   float64 `json:"delayMostMs"`
	// TempoByMinute and DelayByMinute are the tempo the pulses that came back made, and their median delay, minute by minute: a clock that
	// drifts shows as either moving from the first minute to the last
	TempoByMinute []float64 `json:"tempoByMinute"`
	DelayByMinute []float64 `json:"delayByMinute"`
	// WrittenAt and HeardAt are every pulse's moment, nanoseconds on the time thread's timeline: when it was written, when it came back;
	// Messages every message that came back, with its moment, in order
	WrittenAt []int64    `json:"writtenAt"`
	HeardAt   []int64    `json:"heardAt"`
	Messages  []Recorded `json:"messages"`
}

// MeasureClockBack runs a clock of its own hub on the machine's port out, as MeasureClock does, and reads the port back with the
// system's stamps for what comes back, until the clock stops and a moment after.
func MeasureClockBack(ctx context.Context, bpm float64, length time.Duration, out, back string) (ClockMeasure, BackMeasure, error) {
	stamped, ok := any(System{}).(StampedDriver)
	if !ok {
		return ClockMeasure{}, BackMeasure{}, ErrNoPort
	}
	var mu sync.Mutex
	var heard []int64
	running := false
	b := BackMeasure{Back: back}
	input, err := stamped.OpenInStamped(back, func(m Stamped) {
		at, _ := stampOf(m)
		mu.Lock()
		defer mu.Unlock()
		if len(b.Messages) < 200000 {
			b.Messages = append(b.Messages, Recorded{At: at, Driver: m.Driver, Received: m.Received, Message: fmt.Sprintf("%X", m.Message)})
		}
		if len(m.Message) != 1 {
			return
		}
		switch m.Message[0] {
		case 0xFA, 0xFB:
			running = true
			b.Starts++
		case 0xFC:
			running = false
			b.Stops++
		case 0xF8:
			if running {
				heard = append(heard, at)
			}
		}
	})
	if err != nil {
		return ClockMeasure{}, BackMeasure{}, err
	}
	m, err := MeasureClock(ctx, bpm, length, out)
	// what is still on its way back
	time.Sleep(500 * time.Millisecond)
	_ = input.Close()
	if err != nil {
		return m, b, err
	}
	mu.Lock()
	defer mu.Unlock()
	written := m.written
	b.Written, b.Heard, b.Lost = len(written), len(heard), len(written)-len(heard)
	b.WrittenAt, b.HeardAt = written, heard
	b.Measure = MeasureOf(heard, bpm, Timing{})
	var delays []float64
	for i := 0; i < len(heard) && i < len(written); i++ {
		delays = append(delays, float64(heard[i]-written[i])/1e6)
	}
	q := func(values []float64, p float64) float64 {
		if len(values) == 0 {
			return 0
		}
		sorted := append([]float64(nil), values...)
		sort.Float64s(sorted)
		return sorted[int(math.Min(float64(len(sorted)-1), p*float64(len(sorted))))]
	}
	b.DelayMedianMs, b.DelayP99Ms, b.DelayMostMs = q(delays, 0.5), q(delays, 0.99), q(delays, 1)
	perMinute := int(bpm * 24)
	for from := 0; from+perMinute <= len(heard); from += perMinute {
		to := from + perMinute - 1
		b.TempoByMinute = append(b.TempoByMinute, math.Round(60000/(float64(heard[to]-heard[from])/1e6/float64(to-from)*24)*1000)/1000)
		if to < len(delays) {
			b.DelayByMinute = append(b.DelayByMinute, math.Round(q(delays[from:to+1], 0.5)*1000)/1000)
		}
	}
	return m, b, nil
}
