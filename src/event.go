package src

import (
	"errors"
	"fmt"
)

// The most a system exclusive message carries, its F0 and F7 apart.
const maxSysex = 1024

// Event is one MIDI 1.0 message as a listener writes and reads it. Channels are 1 to 16, as players count them.
type Event struct {
	Type       string `json:"type"`
	Channel    int    `json:"channel,omitempty"`
	Note       *int   `json:"note,omitempty"`
	Velocity   *int   `json:"velocity,omitempty"`
	Controller *int   `json:"controller,omitempty"`
	Value      *int   `json:"value,omitempty"`
	Program    *int   `json:"program,omitempty"`
	Bend       *int   `json:"bend,omitempty"`
	Pressure   *int   `json:"pressure,omitempty"`
	Data       []int  `json:"data,omitempty"`
	// At is milliseconds ahead of the call, when the event is to be played; 0 plays it at once. Never on an event that came in.
	At int `json:"at,omitempty"`
	// a followed clock's events (type "clock", ADR-0004 part E), heard by listeners, never played: Clock is what happened (start, stop,
	// continue, position, tempo, lost, back, count, beat, bar); Tempo in beats a minute; Position a Song Position in sixteenths; Bar and
	// Beat counted from 1 under the follow's meter; Heard and Sent the pulses since Start when they differ
	Clock    string  `json:"clock,omitempty"`
	Tempo    float64 `json:"tempo,omitempty"`
	Position *int    `json:"position,omitempty"`
	Bar      *int    `json:"bar,omitempty"`
	Beat     *int    `json:"beat,omitempty"`
	Heard    *int    `json:"heard,omitempty"`
	Sent     *int    `json:"sent,omitempty"`
}

func intp(v int) *int { return &v }

func need(name string, v *int, low, high int) (byte, error) {
	if v == nil {
		return 0, fmt.Errorf("%s is missing", name)
	}
	if *v < low || *v > high {
		return 0, fmt.Errorf("%s is %d to %d", name, low, high)
	}
	return byte(*v), nil
}

// Encode writes an event as the bytes of its MIDI message, every field checked.
func Encode(e Event) ([]byte, error) {
	if e.At < 0 || e.At > 60_000 {
		return nil, errors.New("at is 0 to 60000 milliseconds ahead")
	}
	channelled := map[string]byte{
		"noteOff": 0x80, "noteOn": 0x90, "polyPressure": 0xA0, "cc": 0xB0, "program": 0xC0, "channelPressure": 0xD0, "pitchBend": 0xE0,
	}
	if status, ok := channelled[e.Type]; ok {
		if e.Channel < 1 || e.Channel > 16 {
			return nil, errors.New("channel is 1 to 16")
		}
		status |= byte(e.Channel - 1)
		switch e.Type {
		case "noteOn", "noteOff":
			note, err := need("note", e.Note, 0, 127)
			if err != nil {
				return nil, err
			}
			velocity, err := need("velocity", e.Velocity, 0, 127)
			if e.Velocity == nil && e.Type == "noteOff" {
				velocity, err = 0, nil
			}
			if err != nil {
				return nil, err
			}
			return []byte{status, note, velocity}, nil
		case "polyPressure":
			note, err := need("note", e.Note, 0, 127)
			if err != nil {
				return nil, err
			}
			pressure, err := need("pressure", e.Pressure, 0, 127)
			if err != nil {
				return nil, err
			}
			return []byte{status, note, pressure}, nil
		case "cc":
			controller, err := need("controller", e.Controller, 0, 127)
			if err != nil {
				return nil, err
			}
			value, err := need("value", e.Value, 0, 127)
			if err != nil {
				return nil, err
			}
			return []byte{status, controller, value}, nil
		case "program":
			program, err := need("program", e.Program, 0, 127)
			if err != nil {
				return nil, err
			}
			return []byte{status, program}, nil
		case "channelPressure":
			pressure, err := need("pressure", e.Pressure, 0, 127)
			if err != nil {
				return nil, err
			}
			return []byte{status, pressure}, nil
		default: // pitchBend, -8192 to 8191, centre 0
			if e.Bend == nil || *e.Bend < -8192 || *e.Bend > 8191 {
				return nil, errors.New("bend is -8192 to 8191")
			}
			v := *e.Bend + 8192
			return []byte{status, byte(v & 0x7F), byte(v >> 7)}, nil
		}
	}
	switch e.Type {
	case "start":
		return []byte{0xFA}, nil
	case "continue":
		return []byte{0xFB}, nil
	case "stop":
		return []byte{0xFC}, nil
	case "sysex":
		if len(e.Data) == 0 || len(e.Data) > maxSysex {
			return nil, fmt.Errorf("data is 1 to %d bytes", maxSysex)
		}
		out := []byte{0xF0}
		for _, b := range e.Data {
			if b < 0 || b > 127 {
				return nil, errors.New("a system exclusive's data bytes are 0 to 127")
			}
			out = append(out, byte(b))
		}
		return append(out, 0xF7), nil
	}
	return nil, fmt.Errorf("there is no event %q", e.Type)
}

// Decode reads one whole message as an event; ok is false for a message a listener is not given (clock, active sensing, the rest of
// the system's) or one that does not read.
func Decode(m []byte) (Event, bool) {
	if len(m) == 0 {
		return Event{}, false
	}
	status := m[0]
	if status == 0xF0 {
		if len(m) < 2 || m[len(m)-1] != 0xF7 {
			return Event{}, false
		}
		data := make([]int, 0, len(m)-2)
		for _, b := range m[1 : len(m)-1] {
			data = append(data, int(b))
		}
		return Event{Type: "sysex", Data: data}, true
	}
	switch status {
	case 0xFA:
		return Event{Type: "start"}, true
	case 0xFB:
		return Event{Type: "continue"}, true
	case 0xFC:
		return Event{Type: "stop"}, true
	}
	if status < 0x80 || status >= 0xF0 {
		return Event{}, false
	}
	channel := int(status&0x0F) + 1
	length := dataLength(status)
	if len(m) != 1+length {
		return Event{}, false
	}
	switch status & 0xF0 {
	case 0x80:
		return Event{Type: "noteOff", Channel: channel, Note: intp(int(m[1])), Velocity: intp(int(m[2]))}, true
	case 0x90:
		// a note on with velocity 0 is a note off, as MIDI says
		if m[2] == 0 {
			return Event{Type: "noteOff", Channel: channel, Note: intp(int(m[1])), Velocity: intp(0)}, true
		}
		return Event{Type: "noteOn", Channel: channel, Note: intp(int(m[1])), Velocity: intp(int(m[2]))}, true
	case 0xA0:
		return Event{Type: "polyPressure", Channel: channel, Note: intp(int(m[1])), Pressure: intp(int(m[2]))}, true
	case 0xB0:
		return Event{Type: "cc", Channel: channel, Controller: intp(int(m[1])), Value: intp(int(m[2]))}, true
	case 0xC0:
		return Event{Type: "program", Channel: channel, Program: intp(int(m[1]))}, true
	case 0xD0:
		return Event{Type: "channelPressure", Channel: channel, Pressure: intp(int(m[1]))}, true
	default:
		return Event{Type: "pitchBend", Channel: channel, Bend: intp(int(m[1]) | int(m[2])<<7 - 8192)}, true
	}
}

// dataLength is how many data bytes follow a channel status.
func dataLength(status byte) int {
	switch status & 0xF0 {
	case 0xC0, 0xD0:
		return 1
	default:
		return 2
	}
}

// Parser splits a stream of bytes from a port into whole messages: running status kept, real-time bytes passed through wherever they
// fall, system exclusive gathered up to its bound and dropped past it.
type Parser struct {
	status  byte
	data    []byte
	sysex   []byte
	inSysex bool
}

// Feed takes bytes and calls out with each whole message.
func (p *Parser) Feed(bytes []byte, out func([]byte)) {
	for _, b := range bytes {
		switch {
		case b >= 0xF8: // real-time: whole on its own, even inside another message
			out([]byte{b})
		case b == 0xF0:
			p.inSysex, p.sysex = true, []byte{0xF0}
		case b == 0xF7:
			if p.inSysex && len(p.sysex) <= maxSysex+1 {
				out(append(p.sysex, 0xF7))
			}
			p.inSysex, p.sysex = false, nil
		case b >= 0xF1: // system common: not passed on, and ends running status
			p.status, p.data, p.inSysex = 0, nil, false
		case b >= 0x80:
			p.status, p.data, p.inSysex = b, nil, false
		case p.inSysex:
			if len(p.sysex) <= maxSysex+1 {
				p.sysex = append(p.sysex, b)
			}
		case p.status != 0:
			p.data = append(p.data, b)
			if len(p.data) == dataLength(p.status) {
				out(append([]byte{p.status}, p.data...))
				p.data = nil
			}
		}
	}
}
