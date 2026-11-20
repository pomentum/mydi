package mydi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// System is the machine's MIDI on Linux and the Pi: ALSA's raw MIDI device files, read and written as bytes (ADR-0002 part E). A port
// is a card's device, named by the card's id: `<card id> <device>`.
type System struct{}

var rawDevice = regexp.MustCompile(`^midiC(\d+)D(\d+)$`)

type alsaPort struct {
	PortInfo
	path string
}

func (System) ports() ([]alsaPort, error) {
	entries, err := os.ReadDir("/dev/snd")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ports []alsaPort
	for _, entry := range entries {
		match := rawDevice.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		card, device := match[1], match[2]
		id, _ := os.ReadFile(filepath.Join("/proc/asound", "card"+card, "id"))
		name := fmt.Sprintf("%s %s", strings.TrimSpace(string(id)), device)
		if strings.TrimSpace(string(id)) == "" {
			name = fmt.Sprintf("hw:%s,%s", card, device)
		}
		ports = append(ports, alsaPort{PortInfo: PortInfo{ID: IDOf(name), Name: name, In: true, Out: true}, path: filepath.Join("/dev/snd", entry.Name())})
	}
	return ports, nil
}

// Ports lists the raw MIDI devices; each may be read and written.
func (s System) Ports() ([]PortInfo, error) {
	ports, err := s.ports()
	out := make([]PortInfo, 0, len(ports))
	for _, p := range ports {
		out = append(out, p.PortInfo)
	}
	return out, err
}

func (s System) path(id string) (string, error) {
	ports, err := s.ports()
	if err != nil {
		return "", err
	}
	for _, p := range ports {
		if p.ID == id {
			return p.path, nil
		}
	}
	return "", ErrNoPort
}

type alsaOut struct {
	mu   sync.Mutex
	file *os.File
}

func (o *alsaOut) Write(message []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, err := o.file.Write(message)
	return err
}

func (o *alsaOut) Close() error { return o.file.Close() }

// OpenOut opens a device for writing; ALSA opens a raw device for one direction at a time, so another program may still read it.
func (s System) OpenOut(id string) (Output, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	return &alsaOut{file: file}, nil
}

type alsaIn struct{ file *os.File }

func (i *alsaIn) Close() error { return i.file.Close() }

// OpenIn reads a device's bytes as they come and hands them on; closing the file ends the reading.
func (s System) OpenIn(id string, deliver func([]byte)) (Input, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	go func() {
		buffer := make([]byte, 256)
		for {
			n, err := file.Read(buffer)
			if n > 0 {
				deliver(append([]byte(nil), buffer[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
	return &alsaIn{file: file}, nil
}

// CardID is an ALSA card's id by its number (/proc/asound/card<n>/id), which names its ports; empty when it has none.
func CardID(card string) string {
	id, _ := os.ReadFile(filepath.Join("/proc/asound", "card"+card, "id"))
	return strings.TrimSpace(string(id))
}
