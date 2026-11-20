package mydi

// LanderConfig is ADR-0008 part B's constants: how many of a writer's last Window landed writes may be late before it stands down, and
// for how many writes it stands down, doubled for a stand-down that comes within Window writes of the last one's end, up to MaxDown.
type LanderConfig struct {
	Window, Late, Down, MaxDown int
}

// DefaultLander stands down when more than 2 of the last 96 writes (a bar of pulses) landed late, for 192 writes (2 bars), doubling up
// to 1,536 (16 bars): a try that fails costs at most three late writes, so it tries often, and a machine gone quiet has its landing back
// within seconds (a stand-down of 8 bars doubling to 64 outlasted 30 quiet seconds after 30 loaded ones, 2026-11-16).
var DefaultLander = LanderConfig{Window: 96, Late: 2, Down: 192, MaxDown: 1536}

// Lander decides, write by write, whether a port's writer lands its timed writes on their time (ADR-0008): it lands while it does so
// cleanly, and stands down while it is taken off mid-wait, which costs more than the landing saves.
type Lander struct {
	c      LanderConfig
	window []bool
	at, n  int
	late   int
	// down is how many writes it still stands down for; length the last stand-down's length; since the writes landed since the last one
	// ended, -1 when none has
	down, length, since int
	// StandDowns and StoodDown are its stand-downs and the writes it made standing down
	StandDowns, StoodDown uint64
}

// NewLander is a lander that lands, its window empty.
func NewLander(c LanderConfig) *Lander {
	return &Lander{c: c, window: make([]bool, c.Window), since: -1}
}

// Down says whether it stands down now: the next timed write is written on waking, with no wait on the counter.
func (l *Lander) Down() bool { return l.down > 0 }

// Stood counts one timed write made standing down; the last one ends the stand-down, its window empty.
func (l *Lander) Stood() {
	if l.down == 0 {
		return
	}
	l.down--
	l.StoodDown++
	if l.down == 0 {
		l.since = 0
	}
}

// Landed tells how a write it landed went: late (woken after its time, or written more than 0.3 ms after it) or clean. Past the window's
// limit of late ones it stands down.
func (l *Lander) Landed(late bool) {
	if l.since >= 0 {
		l.since++
	}
	if l.n == len(l.window) {
		if l.window[l.at] {
			l.late--
		}
	} else {
		l.n++
	}
	l.window[l.at] = late
	if late {
		l.late++
	}
	l.at = (l.at + 1) % len(l.window)
	if l.late <= l.c.Late {
		return
	}
	length := l.c.Down
	if l.since >= 0 && l.since <= l.c.Window && l.length > 0 {
		length = min(2*l.length, l.c.MaxDown)
	}
	l.length, l.down, l.since = length, length, -1
	l.StandDowns++
	l.at, l.n, l.late = 0, 0, 0
	clear(l.window)
}
