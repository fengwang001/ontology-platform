// Package ws performs lazy classification of trailing spaces and tabs: a run
// of whitespace is only known to be "trailing" once a line ending or the end
// of stream is seen. Until then it must be buffered.
package ws

// IsSpace reports whether b is a trailing-whitespace candidate (space/tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Tracker records the length of the current undecided whitespace run. A run is
// "active" only when it started at a line boundary and is still uninterrupted;
// whitespace seen after content on the same line is ordinary content.
type Tracker struct {
	// run is the length of the active trailing-whitespace run, or 0.
	run int
}

// Byte feeds a classified byte. content means non-whitespace non-line-ending,
// space means space/tab, eol means a line ending was just emitted.
func (t *Tracker) Byte(kind Kind) int {
	switch kind {
	case KindSpace:
		t.run++
		return 0
	case KindEOL:
		n := t.run
		t.run = 0
		return n // buffered run was trailing: discard it
	default:
		n := t.run
		t.run = 0
		return -n // run was mid-line content: flush it verbatim
	}
}

// Kind is the input category for Byte.
type Kind uint8

const (
	KindContent Kind = iota
	KindSpace
	KindEOL
)

// Run returns the active buffered whitespace length.
func (t *Tracker) Run() int { return t.run }

// Reset clears the tracker at a forced segment boundary.
func (t *Tracker) Reset() { t.run = 0 }
