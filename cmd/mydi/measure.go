package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pomentum/mydi"
)

// runLoopback sends the hub's own pulses out of one port and reads them back on another, wired to it, and says where the system stamps
// input (ADR-0004 part G step 1): on arrival, which a follower may fit on, or on delivery, which it may not. With no output it listens to
// a device's own clock on the input instead.
func runLoopback(args []string) int {
	flags := flag.NewFlagSet("midi loopback", flag.ContinueOnError)
	out := flags.String("out", "", "the port the pulses go out of")
	in := flags.String("in", "", "the port they come back on")
	pulses := flags.Int("pulses", mydi.DefaultLoopback.Pulses, "how many pulses")
	bpm := flags.Float64("bpm", 120, "their tempo, 24 to a beat")
	holdEvery := flags.Int("hold-every", mydi.DefaultLoopback.HoldEvery, "the hub's read held on every this many-th pulse (0: never)")
	holdMs := flags.Int("hold-ms", int(mydi.DefaultLoopback.Hold/time.Millisecond), "for this long")
	seconds := flags.Int("seconds", 30, "with --in alone: how long to listen at most")
	writeHoldEvery := flags.Int("write-hold-every", 0, "the writes held on every this many-th pulse sent (0: never), the owed ones then caught up")
	writeHoldMs := flags.Float64("write-hold-ms", 135, "for this long")
	catchUpMs := flags.Float64("catch-up-ms", 5.2, "the owed pulses this far apart (a writer's catch-up: a quarter interval at 120 BPM)")
	asJSON := flags.Bool("json", false, "the report as JSON, with every pulse's times")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *in == "" || *bpm < 20 || *bpm > 400 || *seconds < 1 {
		fmt.Fprintln(os.Stderr, "usage: mydi loopback [--out <id>] --in <id> [--pulses 480] [--bpm 120] [--hold-every 24] [--hold-ms 30] [--seconds 30] [--json]")
		return 2
	}
	driver, ok := any(mydi.System{}).(mydi.LoopDriver)
	if !ok {
		fmt.Println(mydi.LoopbackReport{Stamps: mydi.StampsNone})
		return 0
	}
	config := mydi.DefaultLoopback
	config.Pulses, config.HoldEvery, config.Hold = *pulses, *holdEvery, time.Duration(*holdMs)*time.Millisecond
	config.WriteHoldEvery = *writeHoldEvery
	config.WriteHold = time.Duration(*writeHoldMs * float64(time.Millisecond))
	config.CatchUp = time.Duration(*catchUpMs * float64(time.Millisecond))
	config.Interval = time.Duration(float64(time.Minute) / (*bpm * 24))
	var run []mydi.LoopbackPulse
	var err error
	if *out == "" {
		run, err = mydi.Listen(driver, *in, config, time.Duration(*seconds)*time.Second)
	} else {
		run, err = mydi.RunLoopback(driver, *out, *in, config)
	}
	// a count that differs is said, with the pulses that paired measured: lost ones fail the run, a device's own do not
	var count *mydi.LoopCount
	if err != nil && !errors.As(err, &count) {
		fmt.Fprintf(os.Stderr, "mydi loopback: %v\n", err)
		return 1
	}
	status := 0
	if count != nil {
		fmt.Fprintf(os.Stderr, "mydi loopback: %v\n", count)
		if count.Lost > 0 {
			status = 1
		}
	}
	report := mydi.AnalyseLoopback(run, config.Interval)
	if *asJSON {
		raw, _ := json.MarshalIndent(map[string]any{"report": report, "pulses": run, "count": count}, "", "  ")
		fmt.Println(string(raw))
		return status
	}
	fmt.Print(report)
	return status
}

// runFollow follows the clock that comes into one port and sends it on, re-made, to others, on this machine's own ports, and reports what
// it did (ADR-0004 step 3): the measure of part G's criteria against a real device, until the time given or an interrupt.
// runTiming is the clock's own measure (ADR-0003): the hub's clock on a stand-in port, and on a real port too when one is named, its
// report as JSON. With --service it answers the service control manager as the service of that name, measures once and stops: ADR-0003's
// service test, the time thread's priority and the process's class as a service gets them.
func runTiming(args []string) int {
	flags := flag.NewFlagSet("midi timing", flag.ContinueOnError)
	bpm := flags.Float64("bpm", 120, "the clock's tempo")
	seconds := flags.Int("seconds", 30, "how long to measure")
	port := flags.String("port", "", "a port of the machine to clock as well, its own cost counted (mydi lists them)")
	out := flags.String("out", "", "a file to write the report to; standard output when none")
	name := flags.String("service", "", "run as the service of this name, started by the service control manager")
	land := flags.Float64("landing", float64(mydi.DefaultLanding)/float64(time.Millisecond), "milliseconds a writer wakes before each write to wait the rest on the counter, standing down while it lands late (ADR-0008); 0: none")
	holdAfter := flags.Int("hold-after", 0, "hold the write of this pulse (from 1), a driver's hold made on purpose, to see what the device makes of the writer's answer")
	holdFor := flags.Int("hold-ms", 700, "with --hold-after: for this long")
	resyncAfter := flags.Float64("resync-after-ms", 0, "how far behind, after a hold, the clock is resynchronised rather than caught up (ADR-0006 part C); 0: the default")
	holdDrop := flags.Bool("hold-drop", false, "past the resync limit, drop the owed pulses with no Stop, Song Position or Continue (ADR-0006 part C)")
	resyncPause := flags.Float64("resync-pause-ms", -1, "how long at least a resync's Continue comes after its Song Position; -1: the default")
	restartAt := flags.Int("restart-at", 0, "stop the clock this many seconds in and start it again in the same process, to tell a clock's own Start from a process's (ADR-0003 part D); the report keeps every pulse's write")
	restartPause := flags.Int("restart-pause", 10, "with --restart-at: seconds the clock stays stopped")
	holdEvery := flags.Int("hold-every", 0, "with --hold-after: hold again every this many pulses")
	holdFlag := flags.String("hold-flag", "", "a file written the moment a write is held past 100 ms: what stops a circular trace just after one (wpr)")
	lead := flags.Bool("lead", false, "each thread's timer aimed early by its own estimate of its wake-up delay (ADR-0003), off by default; true for an A/B")
	back := flags.String("back", "", "with --port: the port the device clocked sends what it receives back on (its soft thru, or a clock send that re-sends each pulse), every pulse counted and stamped as it comes back")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *seconds < 1 || *bpm < 20 || *bpm > 300 || (*back != "" && *port == "") || *land < 0 || *land > 5 {
		fmt.Fprintln(os.Stderr, "usage: mydi timing [--bpm 120] [--seconds 30] [--port <id> [--back <id>]] [--landing <ms>] [--out <file>] [--service <name>]")
		return 2
	}
	mydi.SetLanding(time.Duration(*land * float64(time.Millisecond)))
	mydi.SetLead(*lead)
	mydi.SetHoldDrop(*holdDrop)
	mydi.MeasureRestart.At, mydi.MeasureRestart.Pause = time.Duration(*restartAt)*time.Second, time.Duration(*restartPause)*time.Second
	if *resyncAfter > 0 || *resyncPause >= 0 {
		after, pause := mydi.ResyncDefaults()
		if *resyncAfter > 0 {
			after = time.Duration(*resyncAfter * float64(time.Millisecond))
		}
		if *resyncPause >= 0 {
			pause = time.Duration(*resyncPause * float64(time.Millisecond))
		}
		mydi.SetResync(after, pause)
	}
	mydi.MeasureHold.After, mydi.MeasureHold.Hold, mydi.MeasureHold.Every = *holdAfter, time.Duration(*holdFor)*time.Millisecond, *holdEvery
	if *holdFlag != "" {
		mydi.OnHold = func(heldMs float64) {
			line := fmt.Sprintf("%s a write held %.0f ms so far\n", time.Now().Format(time.RFC3339Nano), heldMs)
			if f, err := os.OpenFile(*holdFlag, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
				_, _ = f.WriteString(line)
				_ = f.Close()
			}
		}
	}
	measure := func(ctx context.Context) error {
		var m mydi.ClockMeasure
		var b *mydi.BackMeasure
		var err error
		tickBefore, coarsest, _, tickKnown := mydi.SystemTick()
		if *back != "" {
			var came mydi.BackMeasure
			m, came, err = mydi.MeasureClockBack(ctx, *bpm, time.Duration(*seconds)*time.Second, *port, *back)
			b = &came
		} else {
			m, err = mydi.MeasureClock(ctx, *bpm, time.Duration(*seconds)*time.Second, *port)
		}
		report := map[string]any{"measure": m, "service": *name != "", "landingMs": *land, "lead": *lead, "at": time.Now().UTC().Format(time.RFC3339)}
		if b != nil {
			report["back"] = b
		}
		// the system's timer resolution at the start and the end, and its coarsest, the default tick (ADR-0003 part B)
		if tickAfter, _, _, ok := mydi.SystemTick(); tickKnown && ok {
			ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
			report["tick"] = map[string]float64{"startMs": ms(tickBefore), "endMs": ms(tickAfter), "defaultMs": ms(coarsest)}
		}
		if err != nil {
			report["error"] = err.Error()
		}
		raw, _ := json.MarshalIndent(report, "", "  ")
		if *out == "" {
			fmt.Println(string(raw))
		} else if werr := os.WriteFile(*out, append(raw, '\n'), 0o644); werr != nil {
			return werr
		}
		return err
	}
	if *name != "" {
		if err := hostService(*name, measure); err != nil {
			fmt.Fprintf(os.Stderr, "mydi timing: %v\n", err)
			return 1
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := measure(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "mydi timing: %v\n", err)
		return 1
	}
	return 0
}

func runFollow(args []string) int {
	flags := flag.NewFlagSet("midi follow", flag.ContinueOnError)
	in := flags.String("in", "", "the port whose clock is followed")
	outs := flags.String("out", "", "the ports the clock is sent to, separated by commas")
	seconds := flags.Int("seconds", 60, "how long to follow at most; an interrupt ends it sooner")
	loopGuard := flags.Bool("loop-guard", false, "the echo test in front of the follower (ADR-0005): on only where the hub's own clock can come back into --in (a soft thru, a chain closed round)")
	asJSON := flags.Bool("json", false, "the report as JSON")
	record := flags.String("record", "", "a file to write every clock and transport message heard and written to, one JSON object a line")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *in == "" || *outs == "" || *seconds < 1 {
		fmt.Fprintln(os.Stderr, "usage: mydi follow --in <id> --out <id>[,<id>] [--seconds 60] [--loop-guard] [--record <file>] [--json]")
		return 2
	}
	config := mydi.RemakeConfig{}
	if *loopGuard {
		c := mydi.DefaultEcho
		config.Echo = &c
	}
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	hub := mydi.NewHub(mydi.System{}, func(string) bool { return true }, nil, logf)
	defer hub.Close()
	following, err := hub.Follow(*in, strings.Split(*outs, ","), config, mydi.DefaultFollow)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mydi follow: %v\n", err)
		return 1
	}
	if *record != "" {
		following.Record()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	end := time.After(time.Duration(*seconds) * time.Second)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	// the port looked at every second, so a device unplugged and plugged back is followed again (ADR-0005)
	look := time.NewTicker(time.Second)
	defer look.Stop()
	fmt.Fprintf(os.Stderr, "following %s into %s for %d s: start the device's transport\n", *in, *outs, *seconds)
wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case <-end:
			break wait
		case <-look.C:
			hub.Revive()
		case <-tick.C:
			r := following.Report()
			fmt.Fprintf(os.Stderr, "  %s at %.2f BPM, %d pulses heard and %d written since Start\n", r.State, r.Tempo, r.Heard, r.Written)
		}
	}
	following.Stop()
	if *record != "" {
		if err := writeRecording(*record, following.Recording()); err != nil {
			fmt.Fprintf(os.Stderr, "mydi follow: the recording: %v\n", err)
			return 1
		}
	}
	report := following.Report()
	timing := hub.Timing()
	if *asJSON {
		raw, _ := json.MarshalIndent(map[string]any{"report": report, "timing": timing}, "", "  ")
		fmt.Println(string(raw))
		return 0
	}
	fmt.Print(followText(report, timing))
	return 0
}

// writeRecording writes a following's recording, one message a line, so a run on a real device can be replayed in a test.
func writeRecording(path string, recording []mydi.Recorded) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(file)
	encoder := json.NewEncoder(w)
	for _, r := range recording {
		if err := encoder.Encode(r); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// ordinal is the suffix of a count read as a place: 1st, 2nd, 3rd, 4th, 11th, 21st.
func ordinal(n uint64) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return "th"
	case n%10 == 1:
		return "st"
	case n%10 == 2:
		return "nd"
	case n%10 == 3:
		return "rd"
	}
	return "th"
}

// followText is a following's report for a person.
func followText(r mydi.FollowReport, t mydi.Timing) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the follower %s at %.2f BPM, its line's spread %.2f ms", r.State, r.Tempo, r.SpreadMs)
	if r.Follower.LockedAfter > 0 {
		fmt.Fprintf(&b, "; it first locked on the source's pulse %d (%.1f beats)", r.Follower.LockedAfter, float64(r.Follower.LockedAfter)/24)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  since the last Start: %d pulses heard, %d written, %d counted by the re-maker; %d added at a Stop, %d caught up after a stall, %d reported after a hold\n",
		r.Heard, r.Written, r.Remaker.Sent, r.Remaker.Added, r.Remaker.CaughtUp, r.Remaker.Reported)
	fmt.Fprintf(&b, "  the source lost %d times, held %d, back %d; %d Stops for a source lost past its hold; %d echoes refused\n",
		r.Follower.Losses, r.Follower.Holds, r.Follower.Reacquired, r.Remaker.StopsOnLoss, r.Remaker.Echoes)
	fmt.Fprintf(&b, "  pulses missing %d; deliveries the system held and let go together %d\n", r.Follower.Missing, r.Follower.Held)
	if r.Returns > 0 {
		fmt.Fprintf(&b, "  the port came back %d times after it was gone, its input opened again\n", r.Returns)
	}
	fmt.Fprintf(&b, "  phase, each pulse written after the source's: median %.2f ms, 99th %.2f, largest %.2f; from the second beat %.2f, %.2f, %.2f\n",
		r.Phase.Median, r.Phase.P99, r.Phase.Max, r.Settled.Phase.Median, r.Settled.Phase.P99, r.Settled.Phase.Max)
	fmt.Fprintf(&b, "  spacing RMS off its median: the source %.2f ms, sent %.2f ms (from the second beat %.2f and %.2f); the source's longest gap %.1f ms\n",
		r.SourceRMS, r.SentRMS, r.Settled.SourceRMS, r.Settled.SentRMS, r.LongestSourceGapMs)
	fmt.Fprintf(&b, "  the input's delivery after the system's stamp: median %.2f ms, 99th %.2f, largest %.2f; stamps refined %d, the system's alone %d, the callback's alone %d\n",
		r.DeliveryLag.Median, r.DeliveryLag.P99, r.DeliveryLag.Max, r.Stamps.Refined, r.Stamps.SystemOnly, r.Stamps.CallbackOnly)
	fmt.Fprintf(&b, "  the time thread (%s): its longest write %.2f ms, %d over 20 ms\n", t.Priority, t.LongestWriteMs, t.SlowWrites)
	return b.String()
}
