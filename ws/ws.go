// Package ws provides the delayed-decision state for trailing spaces and
// tabs. A run of spaces/tabs is held until the next byte proves it is
// interior whitespace (release) or until a line ending/stream end proves
// it is trailing (drop).
package ws

// IsSpace reports whether b is a line-trailing whitespace byte: a space
// or a horizontal tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Hold buffers a not-yet-classified run of spaces and tabs.
type Hold struct {
	bytes  []byte
	orig0  int // original offset of bytes[0]
	active bool
}

// Add starts or extends a hold run. When no run is active the run starts
// at original offset orig0.
func (h *Hold) Add(b byte, orig0 int) {
	if !h.active {
		h.bytes, h.orig0, h.active = h.bytes[:0], orig0, true
	}
	h.bytes = append(h.bytes, b)
}

// Active reports whether a held run exists.
func (h *Hold) Active() bool { return h.active }

// Len is the number of held bytes.
func (h *Hold) Len() int {
	if !h.active {
		return 0
	}
	return len(h.bytes)
}

// Orig0 is the original start offset of the held run.
func (h *Hold) Orig0() int { return h.orig0 }

// Take removes and returns the held bytes and their start offset.
func (h *Hold) Take() ([]byte, int) {
	out, start := h.bytes, h.orig0
	h.bytes, h.active = h.bytes[:0], false
	return out, start
}

// Drop discards the held run (it was trailing whitespace).
func (h *Hold) Drop() { h.bytes, h.active = h.bytes[:0], false }
