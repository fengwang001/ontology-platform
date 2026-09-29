// Package window is a fixed-capacity ring buffer of recent bytes.
package window

import "errors"

// Window keeps at most Cap bytes; older bytes are overwritten.
type Window struct {
	buf  []byte
	next int // index where the next byte is written
	size int // number of valid bytes (<= cap)
}

// New returns a window of the given positive capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, errors.New("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap is the maximum number of bytes retained.
func (w *Window) Cap() int { return len(w.buf) }

// Len is the number of bytes currently stored.
func (w *Window) Len() int { return w.size }

// Reset empties the window.
func (w *Window) Reset() { w.next, w.size = 0, 0 }

// Preset fills the window with an initial dictionary, keeping only its final
// Cap bytes. Used by parallel block compression.
func (w *Window) Preset(dict []byte) {
	w.Reset()
	if len(dict) > len(w.buf) {
		dict = dict[len(dict)-len(w.buf):]
	}
	for _, c := range dict {
		w.buf[w.next] = c
		w.next = (w.next + 1) % len(w.buf)
	}
	w.size = len(dict)
}

// Write appends bytes to the window.
func (w *Window) Write(p []byte) {
	for _, c := range p {
		w.buf[w.next] = c
		w.next = (w.next + 1) % len(w.buf)
	}
	if w.size < len(w.buf) {
		w.size += len(p)
		if w.size > len(w.buf) {
			w.size = len(w.buf)
		}
	}
}

// At returns the byte dist positions back: dist 1 is the most recent byte.
func (w *Window) At(dist int) (byte, bool) {
	if dist <= 0 || dist > w.size {
		return 0, false
	}
	idx := w.next - dist
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx], true
}

// MatchLen compares history starting dist positions back (dist 1 = newest)
// against p[0:], up to max bytes. For indexes i >= dist the candidate refers
// to p[i-dist], so overlapping repetitions compare correctly. Returns the
// matched prefix length.
func (w *Window) MatchLen(dist int, p []byte, max int) int {
	if dist <= 0 || dist > w.size {
		return 0
	}
	start := w.next - dist
	if start < 0 {
		start += len(w.buf)
	}
	n := max
	if n > dist+len(p) {
		n = dist + len(p)
	}
	i := 0
	// History portion: walk the ring directly, handling one wrap.
	hist := dist
	if hist > n {
		hist = n
	}
	for i < hist {
		if w.buf[(start+i)%len(w.buf)] != p[i] {
			return i
		}
		i++
	}
	// Repetition inside p itself.
	for i < n {
		if p[i-dist] != p[i] {
			return i
		}
		i++
	}
	return i
}

// Extend continues a previously established match from index `from`, given a
// grown p. Bytes [0,from) already matched and are not rechecked.
func (w *Window) Extend(dist, from int, p []byte, max int) int {
	if from < dist {
		// Re-enter the ring walk at the resumed offset.
		start := w.next - dist
		if start < 0 {
			start += len(w.buf)
		}
		n := max
		if n > dist+len(p) {
			n = dist + len(p)
		}
		i := from
		hist := dist
		if hist > n {
			hist = n
		}
		for i < hist {
			if w.buf[(start+i)%len(w.buf)] != p[i] {
				return i
			}
			i++
		}
		for i < n {
			if p[i-dist] != p[i] {
				return i
			}
			i++
		}
		return i
	}
	n := max
	if n > dist+len(p) {
		n = dist + len(p)
	}
	for i := from; i < n; i++ {
		if p[i-dist] != p[i] {
			return i
		}
	}
	return n
}
