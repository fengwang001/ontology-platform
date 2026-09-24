// Package ws defers the decision on runs of spaces and tabs:
// a run is trailing only if a line ending or stream end follows it.
package ws

// Tracker holds the current run of spaces/tabs whose fate is undecided.
type Tracker struct {
	run   []byte // pending whitespace; nil when no run is open
	start int    // original offset of the first pending byte
}

// Reset restores the tracker to its initial state.
func (t *Tracker) Reset() { *t = Tracker{} }

// Add appends a whitespace byte at original offset pos.
func (t *Tracker) Add(b byte, pos int) {
	if len(t.run) == 0 {
		t.start = pos
	}
	t.run = append(t.run, b)
}

// Pending reports whether an undecided whitespace run is buffered.
func (t *Tracker) Pending() bool { return len(t.run) > 0 }

// Run returns the buffered bytes and their starting original offset.
func (t *Tracker) Run() (b []byte, start int) { return t.run, t.start }

// Bytes returns the buffered whitespace bytes.
func (t *Tracker) Bytes() []byte { return t.run }

// Keep releases the run as ordinary (mid-line) bytes.
func (t *Tracker) Keep() []byte {
	b := t.run
	t.run, t.start = nil, 0
	return b
}

// Drop discards the run: a line ending or stream end proved it trailing.
func (t *Tracker) Drop() {
	t.run, t.start = nil, 0
}
