// Command mydi measures a machine's MIDI with the mydi library (ADR-0001 part E): its ports listed, its clock's timing measured (as a
// Windows service too), where its system stamps input, and a device's clock followed and sent on.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/pomentum/mydi"
)

const usage = `mydi measures this machine's MIDI.

  mydi                 the machine's MIDI ports, with their ids and directions; opens none
  mydi timing          the hub's clock measured on a stand-in port, and on --port too; --service <name> runs it as a Windows service
  mydi loopback        the hub's pulses out of --out and back on --in, and where the system stamps input; --in alone listens to a
                       device's own clock
  mydi follow          the clock that comes into --in followed and sent on, re-made, to --out

Each opens the ports it names; no other program may hold them. -h after a command lists its flags.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(listPorts())
	}
	switch args[0] {
	case "timing":
		os.Exit(runTiming(args[1:]))
	case "loopback":
		os.Exit(runLoopback(args[1:]))
	case "follow":
		os.Exit(runFollow(args[1:]))
	case "list":
		os.Exit(listPorts())
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

// listPorts prints the machine's MIDI ports, opening none.
func listPorts() int {
	ports, err := mydi.System{}.Ports()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mydi: %v\n", err)
		return 1
	}
	if len(ports) == 0 {
		fmt.Println("no MIDI port on this machine")
		return 0
	}
	for _, p := range ports {
		directions := []string{}
		if p.In {
			directions = append(directions, "in")
		}
		if p.Out {
			directions = append(directions, "out")
		}
		fmt.Printf("%-24s %-8s %s\n", p.ID, strings.Join(directions, ","), p.Name)
	}
	return 0
}
