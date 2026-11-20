package mydi_test

import (
	"math"
	"testing"
	"time"

	"github.com/pomentum/mydi"
)

func TestADrumMachinesHeldDeliveriesKeepTheCount(t *testing.T) {
	// a drum machine's own clock over USB, recorded from a Start to the run's end (2026-11-16): four times after a load ended the system
	// held its input 42 to 58 ms and let the pulses go together, stamped as it did, while the hub's own thread was held as long. The first
	// such gap was taken for two pulses missing, and every pulse after it went out two places ahead of the device's (41 ms early)
	song := recorded(t, "drum-machine-stalls.txt")
	song = append(song, heard{song[len(song)-1].at + 5*ms, []byte{0xFC}})
	// the hub held with its input: from an interval after the last pulse before a gap past 35 ms to 5 ms after the next is stamped
	type hold struct{ from, to int64 }
	var holds []hold
	var device []int64
	last := int64(0)
	for _, h := range song {
		if h.m[0] != 0xF8 {
			continue
		}
		if last > 0 && h.at-last > 35*ms {
			holds = append(holds, hold{last + 21*ms, h.at + 5*ms})
		}
		last = h.at
		device = append(device, h.at)
	}
	if len(holds) != 4 {
		t.Fatalf("%d held deliveries in the recording", len(holds))
	}
	held := func(now int64) bool {
		for _, h := range holds {
			if now >= h.from && now < h.to {
				return true
			}
		}
		return false
	}
	re := mydi.NewRemaker(mydi.NewFollower(mydi.DefaultFollow), mydi.RemakeConfig{})
	var sent []int64
	running := false
	i := 0
	for now := song[0].at; i < len(song); now += ms / 4 {
		if held(now) {
			continue
		}
		var sends []mydi.Send
		for ; i < len(song) && song[i].at <= now; i++ {
			sends = append(sends, re.Message(song[i].m, song[i].at)...)
		}
		sends = append(sends, re.Due(now)...)
		for _, s := range sends {
			switch s.Message[0] {
			case 0xFA:
				running = true
			case 0xFC:
				running = false
			case 0xF8:
				if running {
					sent = append(sent, s.At)
				}
			}
		}
	}
	if len(sent) != len(device) {
		t.Fatalf("%d pulses heard, %d sent: %+v", len(device), len(sent), re.Counts())
	}
	// each pulse a beat past each hold on the device's own, by place
	worst := 0.0
	for k := range device {
		near := false
		for _, h := range holds {
			if device[k] >= h.from-int64(time.Second/2) && device[k] < h.to+int64(time.Second/2) {
				near = true
			}
		}
		if !near && k >= 24 {
			worst = math.Max(worst, math.Abs(float64(sent[k]-device[k])))
		}
	}
	if worst > 3e6 {
		t.Fatalf("away from the holds, a pulse %.2f ms off the device's", worst/1e6)
	}
	t.Logf("%d pulses heard and sent through %d held deliveries; away from them at most %.2f ms off the device's; %+v", len(device),
		len(holds), worst/1e6, re.Counts())
}
