// Package ws delays the classification of trailing spaces and tabs.
// A run of spaces/tabs is only known to be "trailing" once a line ending
// or the stream end is reached; until then the bytes are buffered upstream.
package ws

// Tracker tracks a single contiguous run of spaces (' ') and tabs ('\t')
// at the end of the bytes observed since the last reset. Single-use per
// logical line; not safe for concurrent use.
type Tracker struct {
	n   int    // buffered run length in bytes
	buf []byte // buffered run contents
}

func New() *Tracker { return &Tracker{} }

// IsSpace reports whether b is a trailing-space candidate (space or tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Pending reports the length of the buffered unclassified run.
func (t *Tracker) Pending() int { return t.n }

// Append buffers a space/tab byte.
func (t *Tracker) Append(b byte) {
	t.n++
	t.buf = append(t.buf, b)
}

// Bytes returns the buffered run; valid until the next mutating call.
func (t *Tracker) Bytes() []byte { return t.buf }

// Resolve is called when the run's fate is known: keep=true emits the run
// as ordinary content (stream end without a line ending, or non-space byte
// follows); keep=false drops it as trailing whitespace. It returns the run
// bytes and resets the tracker.
func (t *Tracker) Resolve(keep bool) []byte {
	out := t.buf
	t.n = 0
	t.buf = t.buf[:0]
	if !keep {
		return nil
	}
	return append([]byte(nil), out...)
}
