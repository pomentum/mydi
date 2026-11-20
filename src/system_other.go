//go:build !linux && !windows

package src

// System on macOS and the rest: no ports yet (CoreMIDI when macOS is more than development, ADR-0002 part E).
type System struct{}

// Ports lists none.
func (System) Ports() ([]PortInfo, error) { return nil, nil }

// OpenOut has no port to open.
func (System) OpenOut(string) (Output, error) { return nil, ErrNoPort }

// OpenIn has no port to open.
func (System) OpenIn(string, func([]byte)) (Input, error) { return nil, ErrNoPort }

// CardID has no meaning here: no ALSA card.
func CardID(string) string { return "" }
