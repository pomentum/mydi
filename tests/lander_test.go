package mydi_test

import (
	"testing"

	"github.com/pomentum/mydi"
)

// standDown lands writes until the lander stands down, each late as late says; it answers after how many writes it stood down, or -1.
func standDown(l *mydi.Lander, writes int, late func(i int) bool) int {
	for i := 0; i < writes; i++ {
		if l.Down() {
			return i
		}
		l.Landed(late(i))
	}
	if l.Down() {
		return writes
	}
	return -1
}

// standFor counts the writes a stand-down lasts.
func standFor(l *mydi.Lander) int {
	n := 0
	for l.Down() {
		l.Stood()
		n++
	}
	return n
}

func TestALandingStandsDownWhenItLandsLateAndComesBack(t *testing.T) {
	// ADR-0008 part B on a small window: more than 2 of the last 10 late stands down for 20 writes, doubling to 80
	c := mydi.LanderConfig{Window: 10, Late: 2, Down: 20, MaxDown: 80}
	never := func(int) bool { return false }

	// clean writes never stand it down, nor two late in every ten, spread out
	l := mydi.NewLander(c)
	if at := standDown(l, 1000, never); at != -1 {
		t.Fatalf("clean writes stood it down after %d", at)
	}
	if at := standDown(l, 1000, func(i int) bool { return i%5 == 0 }); at != -1 {
		t.Fatalf("two late in every ten stood it down after %d", at)
	}

	// three late within ten: down for 20 writes, each counted, then landing again with its window empty
	l = mydi.NewLander(c)
	if at := standDown(l, 100, func(i int) bool { return i >= 50 && i%2 == 0 }); at != 55 {
		t.Fatalf("stood down after %d writes, not on the third late one", at)
	}
	if n := standFor(l); n != 20 || l.StandDowns != 1 || l.StoodDown != 20 {
		t.Fatalf("stood down for %d writes, %d times, %d counted", n, l.StandDowns, l.StoodDown)
	}

	// late again at once, within the window after it ended: twice as long, each time, up to 80
	for _, want := range []int{40, 80, 80} {
		if at := standDown(l, 100, func(int) bool { return true }); at != 3 {
			t.Fatalf("late at once stood it down after %d", at)
		}
		if n := standFor(l); n != want {
			t.Fatalf("a stand-down soon after the last lasted %d writes, not %d", n, want)
		}
	}

	// clean for longer than the window, then late: back to 20
	if at := standDown(l, 15, never); at != -1 {
		t.Fatalf("clean writes stood it down after %d", at)
	}
	if at := standDown(l, 100, func(int) bool { return true }); at != 3 {
		t.Fatalf("late stood it down after %d", at)
	}
	if n := standFor(l); n != 20 {
		t.Fatalf("a stand-down long after the last lasted %d writes, not 20", n)
	}
	t.Logf("stood down %d times, %d writes in all", l.StandDowns, l.StoodDown)
}

func TestTheWritersLandByDefault(t *testing.T) {
	// ADR-0008 part A: a writer lands its timed writes, on and off by its lander; a clock on a stand-in port lands each pulse
	if mydi.DefaultLanding <= 0 {
		t.Fatal("no landing by default")
	}
	h := mydi.NewHub(&fake{}, only("synth"), nil, nil)
	defer h.Close()
	if err := h.Clock("synth", 240, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, p := range h.Timing().Ports {
			if p.ID == "synth" && p.Landed+p.StoodDown >= 48 {
				return true
			}
		}
		return false
	})
	_ = h.Clock("synth", 0, false)
	for _, p := range h.Timing().Ports {
		if p.ID == "synth" {
			t.Logf("landed %d, woke late %d, landed late %d, stood down %d times for %d writes", p.Landed, p.WokeLate, p.LandedLate,
				p.StandDowns, p.StoodDown)
		}
	}
}
