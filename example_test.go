package mydi_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pomentum/mydi"
)

// A note played and ended half a second later on a virtual port, whose output is a file: the same calls play a machine's port with
// mydi.System{} as the driver.
func Example() {
	dir, _ := os.MkdirTemp("", "mydi-example")
	defer os.RemoveAll(dir)
	driver := &mydi.Virtual{Dir: dir, Names: []string{"synth"}}
	hub := mydi.NewHub(driver, func(string) bool { return true }, func(mydi.Received) {}, func(string, ...any) {})

	note, velocity := 60, 100
	err := hub.Send("song", "synth", []mydi.Event{
		{Type: "noteOn", Channel: 1, Note: &note, Velocity: &velocity},
		{Type: "noteOff", Channel: 1, Note: &note, At: 500},
	})
	fmt.Println("sent:", err)
	time.Sleep(700 * time.Millisecond)
	hub.Close()

	written, _ := os.ReadFile(filepath.Join(dir, "synth.out.jsonl"))
	fmt.Println("lines written:", strings.Count(string(written), "\n"))
	// Output:
	// sent: <nil>
	// lines written: 2
}

// Encode and Decode turn an event into its MIDI bytes and back.
func ExampleEncode() {
	note, velocity := 64, 90
	bytes, _ := mydi.Encode(mydi.Event{Type: "noteOn", Channel: 2, Note: &note, Velocity: &velocity})
	fmt.Printf("% X\n", bytes)
	event, _ := mydi.Decode(bytes)
	fmt.Println(event.Type, event.Channel, *event.Note, *event.Velocity)
	// Output:
	// 91 40 5A
	// noteOn 2 64 90
}
