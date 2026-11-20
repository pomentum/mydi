package src

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// FollowOptions are what a listener's follow asks (ADR-0004 parts A and E, ADR-0005 part B).
type FollowOptions struct {
	// Hold is how many beats a lost source's tempo is held before the followed ports are stopped, 1 to 64; 0 keeps the default (eight)
	Hold int
	// Meter is the bar's beats over the note a beat is, "4/4" unless set: bars and Song Positions are counted under it, since MIDI clock
	// carries no time signature
	Meter string
	// Beats is what of the song's place listeners hear: "" nothing, "bar" each bar, "beat" each beat
	Beats string
	// LoopGuard turns the echo test on, for a port the hub's own clock can come back into (a synth's soft thru, a chain closed round)
	LoopGuard bool
}

// meterOf reads a meter: 1 to 32 beats, each a whole to a thirty-second note; "" is 4/4.
func meterOf(meter string) (beats, unit int, err error) {
	if meter == "" {
		return 4, 4, nil
	}
	top, bottom, ok := strings.Cut(meter, "/")
	b, err1 := strconv.Atoi(top)
	u, err2 := strconv.Atoi(bottom)
	if !ok || err1 != nil || err2 != nil || b < 1 || b > 32 || (u != 1 && u != 2 && u != 4 && u != 8 && u != 16 && u != 32) {
		return 0, 0, errors.New("meter is beats over a note, as 4/4, 3/4 or 6/8")
	}
	return b, u, nil
}

// check says what is wrong with the options, if anything.
func (o FollowOptions) check() error {
	if o.Hold < 0 || o.Hold > 64 {
		return errors.New("hold is 1 to 64 beats")
	}
	if o.Beats != "" && o.Beats != "bar" && o.Beats != "beat" {
		return errors.New("beats is bar or beat, or nothing")
	}
	_, _, err := meterOf(o.Meter)
	return err
}

// teller turns what a following does into the events listeners hear (part E): the source's transport, its tempo when it moves past a
// band the estimate's own spread sets, its loss and return, a difference in the count, and, when asked, its bars or beats. They are
// informational: a listener hears them late by its own event loop and never plays notes on them.
type teller struct {
	perBeat int64 // pulses a beat: 24 a quarter note
	inBar   int64 // beats a bar
	beats   string
	// song is the song's pulses before the count since Start or Continue: 0 at Start, a Song Position's, or where a Stop left it
	song int64
	// seen is the re-maker's count of pulses sent, as last looked at; told the tempo last told (0: none); heard the pulses heard since
	// the tempo was last looked at
	seen  int64
	told  float64
	heard int
	// toldBand is the band of the estimate told, which a beat after lock is wider than a later one's
	toldBand float64
	// what the follower and the re-maker had last time they were looked at
	state                 string
	stopsOnLoss, reported int64
}

func newTeller(o FollowOptions) teller {
	beats, unit, _ := meterOf(o.Meter)
	return teller{perBeat: int64(96 / unit), inBar: int64(beats), beats: o.Beats, state: FollowAcquiring}
}

func clockEvent(what string) Event { return Event{Type: "clock", Clock: what} }

// place is a pulse's bar and beat, counted from 1, under the meter: index the song's pulse, from 0.
func (t *teller) place(index int64) (bar, beat int) {
	b := index / t.perBeat
	return int(b/t.inBar) + 1, int(b%t.inBar) + 1
}

// message is the source's transport as it came, and what the re-maker sent for it; called before look.
func (t *teller) message(message []byte, sends []Send, wasRunning bool, sent int64) []Event {
	switch message[0] {
	case 0xFA:
		t.song, t.seen = 0, 0
		return []Event{clockEvent("start")}
	case 0xFB:
		t.seen = 0
		return []Event{clockEvent("continue")}
	case 0xFC:
		if wasRunning {
			t.song += sent
		}
		t.seen = 0
		return []Event{clockEvent("stop")}
	case 0xF2:
		// sent on only while stopped (part D): one that came while running is dropped and not told
		if len(sends) == 0 || len(message) < 3 {
			return nil
		}
		position := int(message[1]) | int(message[2])<<7
		t.song = int64(position) * 6
		e := clockEvent("position")
		bar, beat := t.place(t.song)
		e.Position, e.Bar, e.Beat = &position, &bar, &beat
		return []Event{e}
	case 0xF8:
		t.heard++
	}
	return nil
}

// look tells what changed since it last looked: called after each message and each turn of the time thread.
func (t *teller) look(r *Remaker) []Event {
	var out []Event
	state := r.f.State()
	switch {
	case state == FollowHolding && t.state == FollowLocked:
		out = append(out, clockEvent("lost"))
	case t.state == FollowLost && state != FollowLost:
		out = append(out, clockEvent("back"))
	}
	t.state = state
	c := r.Counts()
	if c.StopsOnLoss > t.stopsOnLoss {
		t.song += r.sent
		out = append(out, clockEvent("stop"))
	}
	t.stopsOnLoss = c.StopsOnLoss
	if c.Reported != t.reported {
		e := clockEvent("count")
		heard, sent := int(c.Received), int(c.Sent)
		e.Heard, e.Sent = &heard, &sent
		out = append(out, e)
	}
	t.reported = c.Reported
	// the bars or beats of the pulses sent since it last looked, each on the song's pulse that begins it
	if r.running && t.beats != "" {
		for n := t.seen; n < c.Sent; n++ {
			index := t.song + n
			if index%t.perBeat != 0 {
				continue
			}
			bar, beat := t.place(index)
			if t.beats == "beat" {
				e := clockEvent("beat")
				e.Bar, e.Beat = &bar, &beat
				out = append(out, e)
			} else if beat == 1 {
				e := clockEvent("bar")
				e.Bar = &bar
				out = append(out, e)
			}
		}
	}
	if r.running {
		t.seen = c.Sent
	}
	// the tempo, looked at once a beat of pulses heard while locked
	if state == FollowLocked && (t.told == 0 || t.heard >= 24) {
		t.heard = 0
		// the wider of the two estimates' bands: the one told, from fewer pulses, is the less sure
		if tempo, band := r.f.Tempo(), r.f.tempoBand(); tempo > 0 && (t.told == 0 || math.Abs(tempo-t.told) > math.Max(band, t.toldBand)) {
			t.told, t.toldBand = tempo, band
			e := clockEvent("tempo")
			e.Tempo = math.Round(tempo*100) / 100
			out = append(out, e)
		}
	}
	if state == FollowLost {
		t.told = 0
	}
	return out
}

// tempoBand is how far the tempo must move from the one last told before it is told again: three times the estimate's own standard
// error, from the line's spread over the pulses it was fitted on, and never under 0.1 BPM, a display's step, so the estimate's wander is
// not told as a change while a change the source makes is.
func (f *Follower) tempoBand() float64 {
	n := float64(len(f.points))
	if n < 3 || f.interval <= 0 {
		return 0.1
	}
	slope := f.spread * math.Sqrt(12/(n*n*n))
	return math.Max(0.1, 3*f.Tempo()*slope/f.interval)
}
