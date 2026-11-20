package mydi_test

import (
	"github.com/pomentum/mydi"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stamped is a port that records when each clock pulse reaches it, on the time thread's own timeline (the system's counter on Windows),
// not Go's clock, which moves a system tick at a time there (ADR-0003 part B). With a real port inside it, it stamps a pulse once the
// system's call has returned, so the port's own cost is counted.
type stamped struct {
	mu     sync.Mutex
	pulses []int64
	real   mydi.Output
}

func (s *stamped) Ports() ([]mydi.PortInfo, error) {
	return []mydi.PortInfo{{ID: "synth", Name: "Synth", Out: true}}, nil
}
func (s *stamped) OpenOut(string) (mydi.Output, error)             { return s, nil }
func (s *stamped) OpenIn(string, func([]byte)) (mydi.Input, error) { return nil, mydi.ErrNoPort }
func (s *stamped) Close() error {
	if s.real != nil {
		return s.real.Close()
	}
	return nil
}
func (s *stamped) Write(m []byte) error {
	if s.real != nil {
		if err := s.real.Write(m); err != nil {
			return err
		}
	}
	if len(m) == 1 && m[0] == 0xF8 {
		at := mydi.Now()
		s.mu.Lock()
		s.pulses = append(s.pulses, at)
		s.mu.Unlock()
	}
	return nil
}

// collections is how many times the collector has run in this process.
func collections() uint32 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.NumGC
}

// TestClockTiming measures the clock against its ideal grid: how far each pulse lands from where it should, and the gaps between
// pulses. It depends on the machine and what else runs on it, so it runs only when asked (MYDI_TIMING=1) and fails on nothing
// but a clock that does not run; it is a measure, not a check (ADR-0002 part B, ADR-0003). MYDI_TIMING_LOAD=n adds n goroutines
// that allocate flat out, so the collector runs far more than in a busy program, or together at MYDI_TIMING_MBPS megabytes a
// second; MYDI_TIMING_SECONDS sets its length (20 s). MYDI_TIMING_PORT=<id> plays the clock on a real port of the machine
// (`mydi` lists them), and MYDI_TIMING_NOTES=n writes n note offs a second to it beside the clock, from an ordinary
// goroutine, as a listener's events are; MYDI_TIMING_LIST=n lists the machine's ports every n seconds.
func TestClockTiming(t *testing.T) {
	if os.Getenv("MYDI_TIMING") != "1" {
		t.Skip("a measure of this machine: MYDI_TIMING=1 runs it")
	}
	bpm, seconds := 120.0, 20
	if n, err := strconv.Atoi(os.Getenv("MYDI_TIMING_SECONDS")); err == nil && n > 0 {
		seconds = n
	}
	// MYDI_TIMING_LANDING=µs sets the thread's landing (mydi.SetLanding): the last stretch to each write waited on the counter
	if us, err := strconv.Atoi(os.Getenv("MYDI_TIMING_LANDING")); err == nil && us > 0 {
		mydi.SetLanding(time.Duration(us) * time.Microsecond)
		defer mydi.SetLanding(0)
	}
	var done atomic.Bool
	load := allocating(&done)
	// room for every pulse made first, so the port allocates nothing on the time thread while it is measured
	port := &stamped{pulses: make([]int64, 0, int(bpm*24/60)*(seconds+5))}
	if id := os.Getenv("MYDI_TIMING_PORT"); id != "" {
		real, err := mydi.System{}.OpenOut(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		port.real = real
	}
	h := mydi.NewHub(port, func(string) bool { return true }, nil, nil)
	defer h.Close()
	if err := h.Clock("synth", bpm, true); err != nil {
		t.Fatal(err)
	}
	notes, _ := strconv.Atoi(os.Getenv("MYDI_TIMING_NOTES"))
	var sent atomic.Int64
	if notes > 0 {
		go func() {
			off := []mydi.Event{{Type: "noteOff", Channel: 16, Note: intp(60)}}
			for tick := time.NewTicker(time.Second / time.Duration(notes)); !done.Load(); <-tick.C {
				if h.Send("owner", "synth", off) == nil {
					sent.Add(1)
				}
			}
		}()
	}
	// MYDI_TIMING_LIST=n lists the machine's ports every n seconds beside the clock, as a program's Listen does to hear a device
	// plugged in
	var lists atomic.Int64
	var longestList atomic.Int64
	if every, err := strconv.Atoi(os.Getenv("MYDI_TIMING_LIST")); err == nil && every > 0 {
		go func() {
			for !done.Load() {
				start := mydi.Now()
				_, _ = mydi.System{}.Ports()
				if took := mydi.Now() - start; took > longestList.Load() {
					longestList.Store(took)
				}
				lists.Add(1)
				time.Sleep(time.Duration(every) * time.Second)
			}
		}()
	}
	time.Sleep(time.Duration(seconds) * time.Second)
	_ = h.Clock("synth", 0, false)
	if lists.Load() > 0 {
		t.Logf("the machine's ports listed %d times beside the clock, the longest %.1f ms", lists.Load(), float64(longestList.Load())/1e6)
	}
	done.Store(true)
	if notes > 0 {
		t.Logf("%d note offs written beside the clock", sent.Load())
	}
	port.mu.Lock()
	pulses := append([]int64(nil), port.pulses...)
	port.mu.Unlock()
	if len(pulses) < 100 {
		t.Fatalf("only %d pulses", len(pulses))
	}
	// the measure itself is the package's (mydi.MeasureOf), as `mydi timing` reports it
	m := mydi.MeasureOf(pulses, bpm, h.Timing())
	timing := m.Timing
	t.Logf("%d pulses at %.0f BPM, %d loading goroutines (%s MB/s, 0 is flat out) on %d processors, %d collections",
		m.Pulses, bpm, load, os.Getenv("MYDI_TIMING_MBPS"), runtime.GOMAXPROCS(0), collections())
	t.Logf("port: %s; thread: %s, lead %.3f ms, process %s", map[bool]string{true: os.Getenv("MYDI_TIMING_PORT"), false: "none, stamped only"}[port.real != nil],
		timing.Priority, timing.LeadMs, timing.ProcessClass)
	t.Logf("off the grid: median %.3f ms, p99 %.3f ms, worst %.3f ms", m.OffMedianMs, m.OffP99Ms, m.OffWorstMs)
	t.Logf("gaps: shortest %.3f ms, median %.3f ms, p99 %.3f ms, longest %.3f ms, RMS off the interval %.3f ms",
		m.GapShortestMs, m.GapMedianMs, m.GapP99Ms, m.GapLongestMs, m.GapRMSMs)
	t.Logf("counted by the hub: %d pulses, %d more than 1 ms late, %d more than 5 ms late, longest gap %.3f ms, the grid moved %d times",
		timing.Pulses, timing.Late1ms, timing.Late5ms, timing.LongestGapMs, timing.GridMoves)
	t.Logf("handed to the port's writer and written (ADR-0006): median %.3f ms, p99 %.3f ms over %d writes", timing.HandoffMedianMs,
		timing.HandoffP99Ms, timing.Handoffs)
	// the tempo a receiver reads over each beat: a display that averages one beat shows this wobble, one that averages more shows less
	t.Logf("the tempo beat by beat from the writes: least %.2f, median %.2f, most %.2f BPM; 99%% within %.2f BPM of %.0f",
		m.TempoLeast, m.TempoMedian, m.TempoMost, m.TempoWithin99, bpm)
	t.Logf("drift at the end: %.3f ms", m.DriftMs)
	for _, p := range timing.Ports {
		t.Logf("port %s: wake jitter %.3f ms; landed %d, woke late %d, landed late %d", p.ID, p.WakeJitterMs, p.Landed, p.WokeLate, p.LandedLate)
	}
}

// allocating starts the load MYDI_TIMING_LOAD asks for, n goroutines allocating flat out, or together at MYDI_TIMING_MBPS
// megabytes a second, until done; it answers n.
func allocating(done *atomic.Bool) int {
	load, _ := strconv.Atoi(os.Getenv("MYDI_TIMING_LOAD"))
	var sink atomic.Pointer[[]byte]
	pause := time.Duration(0)
	if rate, err := strconv.Atoi(os.Getenv("MYDI_TIMING_MBPS")); err == nil && rate > 0 && load > 0 {
		// each loader's share of the rate, a 64 KiB allocation at a time
		pause = time.Duration(float64(time.Second) * 64 * 1024 * float64(load) / (float64(rate) * 1e6))
	}
	for range load {
		go func() {
			for !done.Load() {
				b := make([]byte, 64<<10)
				sink.Store(&b)
				if pause > 0 {
					time.Sleep(pause)
				}
			}
		}()
	}
	return load
}
