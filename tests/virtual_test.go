package mydi_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

// The virtual port's files (ADR-0002 part E): an event line played in, a line of bytes as the output file writes them played in as whole
// messages (a clock's pulses and transport, which no event carries, so a follow can be tried on one), and what is sent written out with
// its bytes.
func TestVirtualPortPlaysEventsAndBytesIn(t *testing.T) {
	dir := t.TempDir()
	driver := &mydi.Virtual{Dir: dir, Names: []string{"Drum Machine"}}
	ports, err := driver.Ports()
	if err != nil || len(ports) != 1 || ports[0].ID != "drum-machine" || !ports[0].In || !ports[0].Out {
		t.Fatalf("ports: %+v %v", ports, err)
	}
	var mu sync.Mutex
	var heard [][]byte
	in, err := driver.OpenIn("drum-machine", func(m []byte) {
		mu.Lock()
		heard = append(heard, append([]byte(nil), m...))
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	lines := []string{
		`{"type": "noteOn", "channel": 10, "note": 36, "velocity": 100}`,
		`{"type": "start"}`,
		`{"bytes": [248]}`,
		// as the output file writes a pulse: no event, its byte
		`{"type": "", "bytes": [248], "time": "2026-11-19T00:00:00Z"}`,
		// two messages in one line, the second by running status
		`{"bytes": [144, 60, 100, 64, 90]}`,
		// not a byte: the line is dropped whole
		`{"bytes": [248, 300]}`,
		`{"type": "stop"}`,
	}
	file, err := os.OpenFile(filepath.Join(dir, "Drum Machine.in.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if _, err := file.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	want := [][]byte{{0x99, 36, 100}, {0xFA}, {0xF8}, {0xF8}, {0x90, 60, 100}, {0x90, 64, 90}, {0xFC}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(heard)
		mu.Unlock()
		if n >= len(want) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != len(want) {
		t.Fatalf("heard %v, want %v", heard, want)
	}
	for i := range want {
		if !bytes.Equal(heard[i], want[i]) {
			t.Fatalf("message %d: %v, want %v", i, heard[i], want[i])
		}
	}
}

func TestVirtualPortWritesWhatIsSentWithItsBytes(t *testing.T) {
	dir := t.TempDir()
	driver := &mydi.Virtual{Dir: dir, Names: []string{"synth"}}
	out, err := driver.OpenOut("synth")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range [][]byte{{0x90, 60, 100}, {0xF8}} {
		if err := out.Write(m); err != nil {
			t.Fatal(err)
		}
	}
	out.Close()
	file, err := os.Open(filepath.Join(dir, "synth.out.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	type line struct {
		Type  string `json:"type"`
		Note  *int   `json:"note"`
		Bytes []int  `json:"bytes"`
	}
	var got []line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var l line
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}
	if len(got) != 2 || got[0].Type != "noteOn" || got[0].Note == nil || *got[0].Note != 60 || got[1].Type != "" ||
		len(got[1].Bytes) != 1 || got[1].Bytes[0] != 0xF8 {
		t.Fatalf("written: %+v", got)
	}
	if _, err := driver.OpenOut("piano"); err != mydi.ErrNoPort {
		t.Fatalf("a port it does not have: %v", err)
	}
}
