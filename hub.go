package mydi

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Owner is who sent an event: an owner, such as a client or a song, whose held notes and pending events are its own.
type Owner = string

// Received is what a port played in: the event and when it came.
type Received struct {
	Port  string
	Event Event
	At    time.Time
}

type held struct {
	owner   Owner
	channel byte
	note    byte
}

type out struct {
	mu     sync.Mutex
	output Output
	// writer makes the port's driver calls on a thread of its own (ADR-0006)
	writer *writer
	held   map[held]bool
	used   map[byte]bool
	clock  *clock
	// pending are the events queued on the time thread for this port, kept under the port's lock, so a release never races one
	pending map[*job]bool
}

// clock is a port's running clock: its grid on the timeline, its last pulse, and its one job on the time thread, reused.
type clock struct {
	mu   sync.Mutex
	bpm  float64
	next int64
	last int64
	job  *job
}

func intervalOf(bpm float64) int64 { return int64(float64(time.Minute) / (bpm * 24)) }

// Hub is the hub's MIDI (ADR-0002): the ports a hub may use, opened when first used, events played at the time asked by one time
// thread (ADR-0003), the clock, held notes released, and what comes in handed on with its time.
type Hub struct {
	driver  Driver
	allowed func(id string) bool
	mu      sync.Mutex
	outs    map[string]*out
	ins     map[string]Input
	// follows are the ports followed (ADR-0004): each one's input is its following's, which Listen leaves to it
	follows map[string]*Following
	// followMu keeps a port's follows and unfollows in order: a port follows one source at a time
	followMu sync.Mutex
	listen   func(Received)
	log      func(format string, args ...any)
	stopped  chan struct{}
	sched    *scheduler
	// handoffs is the writers' handoff histogram; writers every port's writer made, for the timing, which takes no port's lock (ADR-0006)
	handoffs  [handoffBuckets]atomic.Uint64
	writersMu sync.Mutex
	writers   []*writer
	// heard is each listened input's clock, under heardMu (Timing's Heard)
	heardMu sync.Mutex
	heard   map[string]*heardClock
	// devices is the machine's ports as Listen last saw them, and their last change (Devices)
	devices devices
}

// heardClock is one input's clock as Listen hears it: whether its transport runs, and its counts.
type heardClock struct {
	running bool
	PortHeard
}

// hear counts one transport or clock message of a listened input.
func (h *Hub) hear(id string, status byte) {
	h.heardMu.Lock()
	defer h.heardMu.Unlock()
	if h.heard == nil {
		h.heard = map[string]*heardClock{}
	}
	c := h.heard[id]
	if c == nil {
		c = &heardClock{PortHeard: PortHeard{ID: id}}
		h.heard[id] = c
	}
	switch status {
	case 0xFA, 0xFB:
		c.running = true
		c.Starts++
	case 0xFC:
		c.running = false
		c.Stops++
	case 0xF8:
		if c.running {
			c.Pulses++
		}
	}
}

// NewHub opens nothing yet; allowed says which ports this hub may use, since a port opened is often a port another program loses.
func NewHub(driver Driver, allowed func(id string) bool, listen func(Received), log func(string, ...any)) *Hub {
	if log == nil {
		log = func(string, ...any) {}
	}
	h := &Hub{
		driver: driver, allowed: allowed, listen: listen, log: log,
		outs: map[string]*out{}, ins: map[string]Input{}, follows: map[string]*Following{}, stopped: make(chan struct{}),
	}
	h.sched = newScheduler(h)
	return h
}

// Timing is how well the hub's time thread has kept time since it started (ADR-0003 part D), with each port's writes and the handoffs to
// them (ADR-0006).
func (h *Hub) Timing() Timing {
	t := h.sched.Timing()
	h.writersMu.Lock()
	writers := append([]*writer(nil), h.writers...)
	h.writersMu.Unlock()
	for _, w := range writers {
		t.Ports = append(t.Ports, w.timing())
	}
	sort.Slice(t.Ports, func(i, j int) bool { return t.Ports[i].ID < t.Ports[j].ID })
	for _, p := range t.Ports {
		t.BlockedMs = max(t.BlockedMs, p.HeldMs)
		t.LongestWriteMs = max(t.LongestWriteMs, p.LongestWriteMs)
		t.SlowWrites += p.SlowWrites
	}
	t.HandoffMedianMs, t.HandoffP99Ms, t.Handoffs = h.handoffSpread()
	h.heardMu.Lock()
	for _, c := range h.heard {
		t.Heard = append(t.Heard, c.PortHeard)
	}
	h.heardMu.Unlock()
	sort.Slice(t.Heard, func(i, j int) bool { return t.Heard[i].ID < t.Heard[j].ID })
	return t
}

// Ports lists the ports this hub may use.
func (h *Hub) Ports() ([]PortInfo, error) {
	all, err := h.driver.Ports()
	if err != nil {
		return nil, err
	}
	return h.allowedOf(all), nil
}

// allowedOf keeps the ports of the machine's this hub may use, sorted.
func (h *Hub) allowedOf(all []PortInfo) []PortInfo {
	ports := []PortInfo{}
	for _, port := range all {
		if h.allowed(port.ID) {
			ports = append(ports, port)
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].ID < ports[j].ID })
	return ports
}

// MachinePorts lists every port the machine has, whatever this hub may use: what a program may offer as targets
// It opens none.
func (h *Hub) MachinePorts() ([]PortInfo, error) {
	return h.driver.Ports()
}

// ErrNotAllowed is a port this hub may not use.
var ErrNotAllowed = errors.New("this hub does not use that MIDI port")

func (h *Hub) output(id string) (*out, error) {
	if !h.allowed(id) {
		return nil, ErrNotAllowed
	}
	h.mu.Lock()
	if o, ok := h.outs[id]; ok && o.output != nil {
		h.mu.Unlock()
		return o, nil
	}
	output, err := h.driver.OpenOut(id)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	o, known := h.outs[id]
	if !known {
		o = &out{held: map[held]bool{}, used: map[byte]bool{}, pending: map[*job]bool{}}
		h.outs[id] = o
	}
	o.mu.Lock()
	h.attachLocked(id, o, output)
	o.mu.Unlock()
	h.mu.Unlock()
	// a port back after it went away may have notes sounding that no one holds now: All Notes Off on the channels it had (part C)
	if known {
		o.mu.Lock()
		channels := make([]byte, 0, len(o.used))
		for c := range o.used {
			channels = append(channels, c)
		}
		o.mu.Unlock()
		for _, c := range channels {
			_ = h.write(id, o, "", []byte{0xB0 | c, 123, 0})
		}
	}
	return o, nil
}

// write sends one message, keeping the held notes; a port that fails is closed, to be opened again when it is back.
func (h *Hub) write(id string, o *out, owner Owner, message []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return h.writeLocked(o, owner, message)
}

// attachLocked makes an opened output the port's, with its writer, with the port's lock held.
func (h *Hub) attachLocked(id string, o *out, output Output) {
	o.output = output
	o.writer = h.startWriter(id, o, output)
}

// writeLocked hands one write to the port's writer with the port's lock held, keeping the held notes: ErrNoPort when the port is not open.
// It never waits on the driver: the writer makes the call (ADR-0006), and a write that fails closes the port there.
func (h *Hub) writeLocked(o *out, owner Owner, message []byte) error {
	return h.handLocked(o, owner, entry{msg: message})
}

// handLocked is writeLocked with what comes back from the write: its done, its following, its clock's interval. A writer whose driver's
// call has held past stuckAfter, or whose ring is full, is given up first (ADR-0006 part D).
func (h *Hub) handLocked(o *out, owner Owner, e entry) error {
	w := o.writer
	if o.output == nil || w == nil {
		return ErrNoPort
	}
	if began := w.busy.Load(); began != 0 && Now()-began > stuckAfter() {
		h.abandon(o, w)
		return ErrNoPort
	}
	e.handed = Now()
	if !w.push(e) {
		h.abandon(o, w)
		return ErrNoPort
	}
	message := e.msg
	if len(message) == 3 && message[0] < 0xF0 {
		kind, channel := message[0]&0xF0, message[0]&0x0F
		o.used[channel] = true
		key := held{owner: owner, channel: channel, note: message[1]}
		switch {
		case kind == 0x90 && message[2] > 0:
			o.held[key] = true
		case kind == 0x80, kind == 0x90:
			delete(o.held, key)
		}
	}
	return nil
}

// Send checks every event first, so nothing half plays, then plays each now or hands it to the time thread for its time; a pending
// event is its owner's, cancelled when the owner is released.
func (h *Hub) Send(owner Owner, id string, events []Event) error {
	messages := make([][]byte, len(events))
	for i, event := range events {
		message, err := Encode(event)
		if err != nil {
			return fmt.Errorf("event %d: %w", i+1, err)
		}
		messages[i] = message
	}
	o, err := h.output(id)
	if err != nil {
		return err
	}
	// every event's time is from one instant, so a batch keeps its own spacing; an event for now is the time thread's too, so the
	// thread is the port's one writer and never waits on a port an ordinary goroutine holds (ADR-0003, its trace), and the call
	// waits for those to be written, so a port that failed is still said to its listener
	start := Now()
	var now []*job
	for i, event := range events {
		j := &job{at: start + int64(event.At)*int64(time.Millisecond), id: id, o: o, owner: owner, message: messages[i], index: -1}
		if event.At == 0 {
			j.done = make(chan error, 1)
			now = append(now, j)
		}
		o.mu.Lock()
		o.pending[j] = true
		o.mu.Unlock()
		h.sched.add(j)
	}
	for _, j := range now {
		select {
		case err := <-j.done:
			if err != nil {
				return err
			}
		case <-h.stopped:
			return ErrNoPort
		}
	}
	return nil
}

// ErrReleased is an event its owner was released before it was played.
var ErrReleased = errors.New("the event was not played: its owner was released first")

// settled tells a caller waiting on a job how it ended, once and without waiting, since the time thread never blocks.
func settled(j *job, err error) {
	if j.done == nil {
		return
	}
	select {
	case j.done <- err:
	default:
	}
}

// play is the time thread's write of a due event, under the port's lock and only if the event is still pending there, so a release that
// took it off first wins.
func (h *Hub) play(j *job) {
	o := j.o
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.pending[j] {
		settled(j, ErrReleased)
		return
	}
	delete(o.pending, j)
	// the writer says how it went to a caller waiting (ADR-0006); a port not open says so now
	if err := h.handLocked(o, j.owner, entry{msg: j.message, done: j.done, due: j.at}); err != nil {
		if j.done == nil {
			h.log("midi: a scheduled event to %s was not played: %v", j.id, err)
		}
		settled(j, err)
	}
}

// cancel takes the pending events of the owners chosen off a port.
func (h *Hub) cancel(o *out, which func(Owner) bool) {
	o.mu.Lock()
	var gone []*job
	for j := range o.pending {
		if which(j.owner) {
			delete(o.pending, j)
			gone = append(gone, j)
		}
	}
	o.mu.Unlock()
	for _, j := range gone {
		h.sched.cancel(j)
		settled(j, ErrReleased)
	}
}

// Release cancels an owner's pending events and releases the notes it holds on every port: a note off for each, then All Notes Off on
// each channel it used (ADR-0002 part C).
func (h *Hub) Release(owner Owner) {
	h.mu.Lock()
	outs := map[string]*out{}
	for id, o := range h.outs {
		outs[id] = o
	}
	h.mu.Unlock()
	for id, o := range outs {
		h.cancel(o, func(who Owner) bool { return who == owner })
		h.release(id, o, func(k held) bool { return k.owner == owner }, false)
	}
}

func (h *Hub) release(id string, o *out, which func(held) bool, everyChannel bool) {
	o.mu.Lock()
	var notes []held
	channels := map[byte]bool{}
	for k := range o.held {
		if which(k) {
			notes = append(notes, k)
			channels[k.channel] = true
		}
	}
	if everyChannel {
		for c := byte(0); c < 16; c++ {
			channels[c] = true
		}
	}
	// handed in one go and waited for, the last one's write meaning all of them (a port's writes keep their order), at most a second
	done := make(chan error, 1)
	var last error
	for i, k := range notes {
		e := entry{msg: []byte{0x80 | k.channel, k.note, 0}}
		if i == len(notes)-1 && len(channels) == 0 {
			e.done = done
		}
		last = h.handLocked(o, k.owner, e)
	}
	i := 0
	for c := range channels {
		i++
		e := entry{msg: []byte{0xB0 | c, 123, 0}}
		if i == len(channels) {
			e.done = done
		}
		last = h.handLocked(o, "", e)
	}
	o.mu.Unlock()
	if (len(notes) > 0 || len(channels) > 0) && last == nil {
		_ = h.waitWritten(done)
	}
}

// Panic releases every held note on a port, whoever played it, and sends All Notes Off on every channel: first, before anything the
// port's writer still holds, whose note-ons it takes out (ADR-0006 part C).
func (h *Hub) Panic(id string) error {
	o, err := h.output(id)
	if err != nil {
		return err
	}
	o.mu.Lock()
	w := o.writer
	messages := make([][]byte, 0, len(o.held)+16)
	for k := range o.held {
		messages = append(messages, []byte{0x80 | k.channel, k.note, 0})
	}
	o.held = map[held]bool{}
	for c := byte(0); c < 16; c++ {
		messages = append(messages, []byte{0xB0 | c, 123, 0})
	}
	o.mu.Unlock()
	if w == nil {
		return ErrNoPort
	}
	w.urgent(messages)
	return nil
}

// Clock starts the clock on a port at a tempo (24 pulses a quarter note, with Start), changes its tempo while it runs, or stops it
// (with Stop). Its pulses are the time thread's (ADR-0003), on a grid kept from its start, so they do not drift with the scheduler.
func (h *Hub) Clock(id string, bpm float64, run bool) error {
	if run && (bpm < 20 || bpm > 300) {
		return errors.New("bpm is 20 to 300")
	}
	// a port followed is never clocked (ADR-0004 part A); a port that follows a source goes back to its own clock, or stops
	h.mu.Lock()
	_, followed := h.follows[id]
	h.mu.Unlock()
	if followed && run {
		return ErrFollowSamePort
	}
	if h.Unfollow(id) && !run {
		return nil
	}
	o, err := h.output(id)
	if err != nil {
		return err
	}
	if !run {
		return h.stopClock(o)
	}
	o.mu.Lock()
	c := o.clock
	if c != nil {
		o.mu.Unlock()
		c.mu.Lock()
		c.bpm = bpm
		c.mu.Unlock()
		return nil
	}
	done := make(chan error, 1)
	if err := h.handLocked(o, "", entry{msg: []byte{0xFA}, done: done}); err != nil {
		o.mu.Unlock()
		return err
	}
	c = &clock{bpm: bpm}
	c.next = Now() + intervalOf(bpm)
	c.job = &job{at: c.next, id: id, o: o, clock: c, index: -1}
	o.clock = c
	o.mu.Unlock()
	h.sched.add(c.job)
	return h.waitWritten(done)
}

// stopClock stops a port's own clock, if it runs, with Stop.
func (h *Hub) stopClock(o *out) error {
	o.mu.Lock()
	c := o.clock
	if c == nil {
		o.mu.Unlock()
		return nil
	}
	// Stop under the port's lock: a pulse checks the port's clock under it too, so none is handed after the Stop; and those handed ahead
	// and still waiting are taken back first, so none is written after it either (ADR-0007)
	o.clock = nil
	if o.writer != nil {
		o.writer.recallTicks()
	}
	done := make(chan error, 1)
	err := h.handLocked(o, "", entry{msg: []byte{0xFC}, done: done})
	o.mu.Unlock()
	h.sched.cancel(c.job)
	if err != nil {
		return err
	}
	return h.waitWritten(done)
}

// waitWritten waits for a write its caller handed, at most a second: a port whose driver holds longer has it written when it returns.
func (h *Hub) waitWritten(done chan error) error {
	select {
	case err := <-done:
		return err
	case <-h.stopped:
		return ErrNoPort
	case <-time.After(time.Second):
		return nil
	}
}

// pulseMessage is a clock pulse, one for every write, so the time thread allocates none.
var pulseMessage = []byte{0xF8}

// tick is the time thread's write of a clock's pulse, under the port's lock and only while this clock is the port's. A port that fails
// ends its clock, to be started again once the port is back.
func (h *Hub) tick(c *clock, due int64) bool {
	o := c.job.o
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.clock != c {
		return false
	}
	c.mu.Lock()
	interval := intervalOf(c.bpm)
	c.mu.Unlock()
	if err := h.handLocked(o, "", entry{msg: pulseMessage, interval: interval, due: due, tick: true}); err != nil {
		o.clock = nil
		h.log("midi: the clock on %s stopped: %v", c.job.id, err)
		return false
	}
	return true
}

// Listen opens the inputs of the ports this hub may use and has not opened, and closes those gone; called again every few seconds,
// so a device plugged in is heard. A followed port's input is its following's: Listen tells it whether the port is there (Revive). It
// notes a change of the machine's ports too (Devices).
func (h *Hub) Listen() {
	all, err := h.driver.Ports()
	if err != nil {
		return
	}
	h.watchDevices(all)
	ports := h.allowedOf(all)
	present := map[string]bool{}
	for _, port := range ports {
		if !port.In {
			continue
		}
		present[port.ID] = true
		h.mu.Lock()
		_, open := h.ins[port.ID]
		_, followed := h.follows[port.ID]
		h.mu.Unlock()
		if open || followed {
			continue
		}
		id := port.ID
		parser := &Parser{}
		var mu sync.Mutex
		input, err := h.driver.OpenIn(id, func(bytes []byte) {
			at := time.Now()
			mu.Lock()
			defer mu.Unlock()
			parser.Feed(bytes, func(message []byte) {
				if len(message) == 1 && (message[0] == 0xF8 || message[0] == 0xFA || message[0] == 0xFB || message[0] == 0xFC) {
					h.hear(id, message[0])
				}
				// clock and active sensing are not a listener's: dozens a second for nothing (part B)
				if len(message) == 1 && (message[0] == 0xF8 || message[0] == 0xFE) {
					return
				}
				if event, ok := Decode(message); ok && h.listen != nil {
					h.listen(Received{Port: id, Event: event, At: at})
				}
			})
		})
		if err != nil {
			h.log("midi: %s could not be opened for input: %v", id, err)
			continue
		}
		h.mu.Lock()
		h.ins[id] = input
		h.mu.Unlock()
		h.log("midi: listening on %s", id)
	}
	h.mu.Lock()
	for id, input := range h.ins {
		if _, followed := h.follows[id]; !present[id] && !followed {
			_ = input.Close()
			delete(h.ins, id)
		}
	}
	h.mu.Unlock()
	h.revive(present)
}

// Revive tells each followed port's following whether its port is there now, so an input whose device was unplugged is closed and
// opened again once the device is back: a followed input does not come back by itself (a cable pulled and put back, 2026-11-15).
// Listen does it; a hub that does not listen calls it every second or so.
func (h *Hub) Revive() {
	ports, err := h.Ports()
	if err != nil {
		return
	}
	present := map[string]bool{}
	for _, port := range ports {
		if port.In {
			present[port.ID] = true
		}
	}
	h.revive(present)
}

func (h *Hub) revive(present map[string]bool) {
	h.mu.Lock()
	follows := make([]*Following, 0, len(h.follows))
	for _, f := range h.follows {
		follows = append(follows, f)
	}
	h.mu.Unlock()
	for _, f := range follows {
		f.watch(present[f.in])
	}
}

// Close stops the time thread and the clocks, releases every held note, and closes every port.
func (h *Hub) Close() {
	select {
	case <-h.stopped:
		return
	default:
		close(h.stopped)
	}
	h.sched.stop()
	h.mu.Lock()
	outs := map[string]*out{}
	for id, o := range h.outs {
		outs[id] = o
	}
	for id, input := range h.ins {
		_ = input.Close()
		delete(h.ins, id)
	}
	h.mu.Unlock()
	for id, o := range outs {
		h.cancel(o, func(Owner) bool { return true })
		o.mu.Lock()
		o.clock = nil
		o.mu.Unlock()
		h.release(id, o, func(held) bool { return true }, false)
		// the writer writes what waits and closes the port; one held in a driver's call past a second is given up (ADR-0006 part D)
		o.mu.Lock()
		w := o.writer
		o.output, o.writer = nil, nil
		o.mu.Unlock()
		if w != nil {
			w.stop()
			select {
			case <-w.exited:
			case <-time.After(time.Second):
				w.abandoned.Store(true)
			}
		}
	}
}
