package src

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// System is the machine's MIDI on Windows: Winmm.dll's multimedia MIDI through Go's system calls, no CGo (ADR-0002 part E). A port is a
// device by its name; system exclusive out is not sent yet (it takes midiOutLongMsg).
type System struct{}

var (
	winmm                 = windows.NewLazySystemDLL("winmm.dll")
	procMidiOutGetNumDevs = winmm.NewProc("midiOutGetNumDevs")
	procMidiOutGetDevCaps = winmm.NewProc("midiOutGetDevCapsW")
	procMidiOutOpen       = winmm.NewProc("midiOutOpen")
	procMidiOutShortMsg   = winmm.NewProc("midiOutShortMsg")
	procMidiOutReset      = winmm.NewProc("midiOutReset")
	procMidiOutClose      = winmm.NewProc("midiOutClose")
	procMidiInGetNumDevs  = winmm.NewProc("midiInGetNumDevs")
	procMidiInGetDevCaps  = winmm.NewProc("midiInGetDevCapsW")
	procMidiInOpen        = winmm.NewProc("midiInOpen")
	procMidiInStart       = winmm.NewProc("midiInStart")
	procMidiInStop        = winmm.NewProc("midiInStop")
	procMidiInClose       = winmm.NewProc("midiInClose")
)

const (
	callbackFunction = 0x00030000
	mimData          = 0x3C3
)

// MIDIOUTCAPSW and MIDIINCAPSW: the name sits after the manufacturer, product and driver version.
type outCaps struct {
	Mid, Pid      uint16
	DriverVersion uint32
	Name          [32]uint16
	Technology    uint16
	Voices        uint16
	Notes         uint16
	ChannelMask   uint16
	Support       uint32
}

type inCaps struct {
	Mid, Pid      uint16
	DriverVersion uint32
	Name          [32]uint16
	Support       uint32
}

type winPort struct {
	PortInfo
	in, out int // device numbers, -1 when the port has no such direction
}

func (System) ports() []winPort {
	byName := map[string]*winPort{}
	var order []string
	add := func(name string) *winPort {
		if p, ok := byName[name]; ok {
			return p
		}
		p := &winPort{PortInfo: PortInfo{ID: IDOf(name), Name: name}, in: -1, out: -1}
		byName[name] = p
		order = append(order, name)
		return p
	}
	n, _, _ := procMidiOutGetNumDevs.Call()
	for i := 0; i < int(n); i++ {
		var caps outCaps
		if r, _, _ := procMidiOutGetDevCaps.Call(uintptr(i), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps)); r == 0 {
			p := add(windows.UTF16ToString(caps.Name[:]))
			p.out, p.Out = i, true
		}
	}
	n, _, _ = procMidiInGetNumDevs.Call()
	for i := 0; i < int(n); i++ {
		var caps inCaps
		if r, _, _ := procMidiInGetDevCaps.Call(uintptr(i), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps)); r == 0 {
			p := add(windows.UTF16ToString(caps.Name[:]))
			p.in, p.In = i, true
		}
	}
	ports := make([]winPort, 0, len(order))
	for _, name := range order {
		ports = append(ports, *byName[name])
	}
	return ports
}

// Ports lists the devices, a name with an input and an output being one port.
func (s System) Ports() ([]PortInfo, error) {
	ports := s.ports()
	out := make([]PortInfo, 0, len(ports))
	for _, p := range ports {
		out = append(out, p.PortInfo)
	}
	return out, nil
}

func (s System) find(id string) (winPort, error) {
	for _, p := range s.ports() {
		if p.ID == id {
			return p, nil
		}
	}
	return winPort{}, ErrNoPort
}

type winOut struct {
	mu     sync.Mutex
	handle uintptr
}

func (o *winOut) Write(message []byte) error {
	if len(message) == 0 || len(message) > 3 || message[0] == 0xF0 {
		return errors.New("system exclusive is not sent on Windows yet")
	}
	var packed uint32
	for i, b := range message {
		packed |= uint32(b) << (8 * i)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// by its address, so the time thread's write allocates nothing (ADR-0003; `LazyProc.Call` boxes its arguments)
	if r, _, _ := syscall.SyscallN(procMidiOutShortMsg.Addr(), o.handle, uintptr(packed)); r != 0 {
		return fmt.Errorf("midiOutShortMsg failed (%d)", r)
	}
	return nil
}

func (o *winOut) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	procMidiOutReset.Call(o.handle)
	if r, _, _ := procMidiOutClose.Call(o.handle); r != 0 {
		return fmt.Errorf("midiOutClose failed (%d)", r)
	}
	return nil
}

// OpenOut opens a device's output.
func (s System) OpenOut(id string) (Output, error) {
	p, err := s.find(id)
	if err != nil || p.out < 0 {
		return nil, ErrNoPort
	}
	var handle uintptr
	if r, _, _ := procMidiOutOpen.Call(uintptr(unsafe.Pointer(&handle)), uintptr(p.out), 0, 0, 0); r != 0 {
		return nil, fmt.Errorf("midiOutOpen failed (%d)", r)
	}
	return &winOut{handle: handle}, nil
}

// An input as its callback finds it: where its messages go, and the timeline's time when WinMM started counting its stamps from zero.
type winInput struct {
	deliver func(Stamped)
	start   atomic.Int64
}

// The inputs' callbacks: one function for every input, which finds its port by the instance number it was opened with. Windows calls
// it on a thread of its own, so it only hands the bytes on, with WinMM's stamp (param2, milliseconds since midiInStart) mapped onto the
// timeline and the time the callback was entered (ADR-0004 part B).
var (
	inputsMu sync.Mutex
	inputs   = map[uintptr]*winInput{}
	nextIn   uintptr
	inProc   = syscall.NewCallback(func(handle, msg, instance, param1, param2 uintptr) uintptr {
		if msg == mimData {
			received := Now()
			inputsMu.Lock()
			in := inputs[instance]
			inputsMu.Unlock()
			if in != nil {
				status := byte(param1)
				message := []byte{status, byte(param1 >> 8), byte(param1 >> 16)}
				switch {
				case status >= 0xF8:
					message = message[:1]
				case status&0xF0 == 0xC0, status&0xF0 == 0xD0:
					message = message[:2]
				}
				in.deliver(Stamped{Message: message, Driver: in.start.Load() + int64(param2)*1e6, Received: received})
			}
		}
		return 0
	})
)

type winIn struct {
	handle   uintptr
	instance uintptr
}

func (i *winIn) Close() error {
	procMidiInStop.Call(i.handle)
	procMidiInClose.Call(i.handle)
	inputsMu.Lock()
	delete(inputs, i.instance)
	inputsMu.Unlock()
	return nil
}

// OpenIn opens a device's input and starts it; each short message is handed on whole.
func (s System) OpenIn(id string, deliver func([]byte)) (Input, error) {
	return s.OpenInStamped(id, func(m Stamped) { deliver(m.Message) })
}

// OpenInStamped opens a device's input and starts it; each short message is handed on with its stamps.
func (s System) OpenInStamped(id string, deliver func(Stamped)) (Input, error) {
	p, err := s.find(id)
	if err != nil || p.in < 0 {
		return nil, ErrNoPort
	}
	in := &winInput{deliver: deliver}
	inputsMu.Lock()
	nextIn++
	instance := nextIn
	inputs[instance] = in
	inputsMu.Unlock()
	var handle uintptr
	if r, _, _ := procMidiInOpen.Call(uintptr(unsafe.Pointer(&handle)), uintptr(p.in), inProc, instance, callbackFunction); r != 0 {
		inputsMu.Lock()
		delete(inputs, instance)
		inputsMu.Unlock()
		return nil, fmt.Errorf("midiInOpen failed (%d)", r)
	}
	// WinMM's stamps count from midiInStart: its moment on the timeline, the middle of the call, within the call's own length
	before := Now()
	in.start.Store(before)
	r, _, _ := procMidiInStart.Call(handle)
	in.start.Store(before + (Now()-before)/2)
	if r != 0 {
		procMidiInClose.Call(handle)
		return nil, fmt.Errorf("midiInStart failed (%d)", r)
	}
	return &winIn{handle: handle, instance: instance}, nil
}

// CardID has no meaning on Windows, which names a port by its device's name.
func CardID(string) string { return "" }
