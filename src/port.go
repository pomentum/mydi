package src

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PortInfo is a port as the machine has it: an id a listener names it by, its name, and its directions.
type PortInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	In   bool   `json:"in"`
	Out  bool   `json:"out"`
}

// Output writes whole messages to a port.
type Output interface {
	Write(message []byte) error
	Close() error
}

// Input is a port open for reading; what it reads goes to the function it was opened with.
type Input interface{ Close() error }

// Driver is the machine's MIDI: its ports, opened by id.
type Driver interface {
	Ports() ([]PortInfo, error)
	OpenOut(id string) (Output, error)
	OpenIn(id string, deliver func(message []byte)) (Input, error)
}

// Stamped is one message read with its times on the time thread's timeline (ADR-0004 part B): Driver, the stamp the system's MIDI took
// for it outside Go (WinMM's, mapped onto the timeline), or -1 where the system gives none; Received, the stamp taken as the hub's
// callback entered, which Go's pauses and the system's delivery can hold back.
type Stamped struct {
	Message  []byte
	Driver   int64
	Received int64
}

// StampedDriver is a driver whose input gives each message its stamps.
type StampedDriver interface {
	OpenInStamped(id string, deliver func(Stamped)) (Input, error)
}

// ErrNoPort is a port the machine does not have now.
var ErrNoPort = errors.New("the machine has no such MIDI port now")

var notID = regexp.MustCompile(`[^a-z0-9]+`)

// IDOf makes a port's id from its name: lower case letters, digits and dashes, which a URL and an `http in` route take as they are.
func IDOf(name string) string {
	id := strings.Trim(notID.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(id) > 63 {
		id = strings.TrimRight(id[:63], "-")
	}
	if id == "" {
		return "port"
	}
	return id
}

// Virtual is a driver of ports that are files (ADR-0002 part E): what is sent to `<name>` is written as a JSON line to
// `<dir>/<name>.out.jsonl`, and a JSON line appended to `<dir>/<name>.in.jsonl` is played in: an event, or a line as the output file
// writes it, whose bytes carry what no event does (a device's clock, so a follow can be tried). For tests, and for trying a listener with
// no device. Beside, when set, is the machine's own driver, whose ports are listed after the virtual ones and opened through it, so a test
// can hear a virtual port and play a real one.
type Virtual struct {
	Dir    string
	Names  []string
	Beside Driver
}

// Ports lists the virtual ports, each both ways, then those of the driver beside them; one that cannot list its ports lists none.
func (v *Virtual) Ports() ([]PortInfo, error) {
	ports := make([]PortInfo, 0, len(v.Names))
	for _, name := range v.Names {
		ports = append(ports, PortInfo{ID: IDOf(name), Name: name, In: true, Out: true})
	}
	if v.Beside != nil {
		if beside, err := v.Beside.Ports(); err == nil {
			for _, port := range beside {
				if _, virtual := v.nameOf(port.ID); !virtual {
					ports = append(ports, port)
				}
			}
		}
	}
	return ports, nil
}

func (v *Virtual) nameOf(id string) (string, bool) {
	for _, name := range v.Names {
		if IDOf(name) == id {
			return name, true
		}
	}
	return "", false
}

type virtualOut struct {
	mu   sync.Mutex
	file *os.File
}

func (o *virtualOut) Write(message []byte) error {
	line := struct {
		Event
		Bytes []int     `json:"bytes"`
		Time  time.Time `json:"time"`
	}{Time: time.Now().UTC()}
	if event, ok := Decode(message); ok {
		line.Event = event
	}
	for _, b := range message {
		line.Bytes = append(line.Bytes, int(b))
	}
	raw, err := json.Marshal(line)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_, err = o.file.Write(append(raw, '\n'))
	return err
}

func (o *virtualOut) Close() error { return o.file.Close() }

// OpenOut opens a virtual port's output file for appending.
func (v *Virtual) OpenOut(id string) (Output, error) {
	name, ok := v.nameOf(id)
	if !ok && v.Beside != nil {
		return v.Beside.OpenOut(id)
	}
	if !ok {
		return nil, ErrNoPort
	}
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(v.Dir, name+".out.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &virtualOut{file: file}, nil
}

type virtualIn struct{ stop chan struct{} }

func (i *virtualIn) Close() error {
	select {
	case <-i.stop:
	default:
		close(i.stop)
	}
	return nil
}

// OpenIn follows a virtual port's input file from its end, every few milliseconds, each line an event played in.
func (v *Virtual) OpenIn(id string, deliver func([]byte)) (Input, error) {
	name, ok := v.nameOf(id)
	if !ok && v.Beside != nil {
		return v.Beside.OpenIn(id, deliver)
	}
	if !ok {
		return nil, ErrNoPort
	}
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(v.Dir, name+".in.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return nil, err
	}
	in := &virtualIn{stop: make(chan struct{})}
	go func() {
		defer file.Close()
		reader := bufio.NewReader(file)
		var parser Parser
		pending := ""
		for {
			select {
			case <-in.stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			for {
				chunk, err := reader.ReadString('\n')
				pending += chunk
				if err != nil {
					break
				}
				var line struct {
					Event
					Bytes []int `json:"bytes"`
				}
				if json.Unmarshal([]byte(strings.TrimSpace(pending)), &line) == nil {
					if line.Type == "" && len(line.Bytes) > 0 {
						// a line as the output file writes it, with no event: its bytes, which carry what no event does (a clock's
						// pulse), split into whole messages as a port's stream is
						raw := make([]byte, 0, len(line.Bytes))
						for _, b := range line.Bytes {
							if b >= 0 && b <= 0xFF {
								raw = append(raw, byte(b))
							}
						}
						if len(raw) == len(line.Bytes) {
							parser.Feed(raw, deliver)
						}
					} else if message, err := Encode(line.Event); err == nil {
						deliver(message)
					}
				}
				pending = ""
			}
		}
	}()
	return in, nil
}
