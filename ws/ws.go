// Package ws defers the classification of trailing spaces and tabs.
// A run of spaces/tabs is buffered until a following byte shows whether it
// sits mid-line (then it is content) or at line end / EOF (then it is
// deleted). A held '\r' (from eol) participates in the same pending queue so
// classification stays identical across any input split points.
package ws

// Pending buffers one unresolved run. Bytes are spaces/tabs optionally
// followed by a single '\r' that is still waiting for its '\n'.
type Pending struct {
	Bytes []byte
	Start int64 // original offset of Bytes[0]
}

// Space reports whether b is trailing-whitespace-eligible (space or tab).
func Space(b byte) bool { return b == ' ' || b == '\t' }

// Tracker holds the currently unresolved run; not safe for concurrent use.
type Tracker struct {
	p Pending
}

// Len is the number of held original bytes.
func (t *Tracker) Len() int { return len(t.p.Bytes) }

// Start is the original offset of the first held byte.
func (t *Tracker) Start() int64 { return t.p.Start }

// Get returns the held run.
func (t *Tracker) Get() Pending { return t.p }

// AddSpace appends a space/tab at original offset pos. It must be called only
// while no line ending intervenes (norm guarantees ordering).
func (t *Tracker) AddSpace(b byte, pos int64) {
	if len(t.p.Bytes) == 0 {
		t.p.Start = pos
	}
	t.p.Bytes = append(t.p.Bytes, b)
}

// AddCR appends a held '\r' at original offset pos. At most one '\r' is held,
// and it always trails any held spaces.
func (t *Tracker) AddCR(pos int64) {
	if len(t.p.Bytes) == 0 {
		t.p.Start = pos
	}
	t.p.Bytes = append(t.p.Bytes, '\r')
}

// Take removes and returns the held run, or an empty run.
func (t *Tracker) Take() Pending {
	p := t.p
	t.p = Pending{}
	return p
}

// Reset clears all state.
func (t *Tracker) Reset() { t.p = Pending{} }
