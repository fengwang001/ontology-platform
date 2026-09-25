// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

// Window keeps the last capacity bytes plus an optional preset dictionary.
// Position numbering is absolute and continuous across pushes, so a byte is
// addressable by distance: distance 1 is the most recently pushed byte.
type Window struct {
	buf      []byte
	pos      []int64 // absolute position of buf[i]
	capacity int
	next     int64 // absolute position of the next byte to be written
	head     int   // ring index where the next byte is written
	count    int   // live bytes
}

// New creates a window with the given capacity and optional preset dictionary.
// Only the last capacity bytes of the dictionary are retained.
func New(capacity int, dict []byte) *Window {
	w := &Window{
		buf:      make([]byte, capacity),
		pos:      make([]int64, capacity),
		capacity: capacity,
	}
	if len(dict) > capacity {
		dict = dict[len(dict)-capacity:]
	}
	for _, c := range dict {
		w.Push(c)
	}
	return w
}

// Capacity reports the configured capacity.
func (w *Window) Capacity() int { return w.capacity }

// Len reports how many live bytes the window holds.
func (w *Window) Len() int { return w.count }

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(c byte) {
	w.buf[w.head] = c
	w.pos[w.head] = w.next
	w.next++
	w.head++
	if w.head == w.capacity {
		w.head = 0
	}
	if w.count < w.capacity {
		w.count++
	}
}

// At returns the byte at distance d (1 = newest). ok is false if d exceeds the
// number of live bytes or the capacity.
func (w *Window) At(d int) (byte, bool) {
	if d <= 0 || d > w.count || d > w.capacity {
		return 0, false
	}
	idx := w.head - d
	if idx < 0 {
		idx += w.capacity
	}
	return w.buf[idx], true
}

// PosOf returns the absolute position of the byte at distance d.
func (w *Window) PosOf(d int) (int64, bool) {
	if d <= 0 || d > w.count || d > w.capacity {
		return 0, false
	}
	idx := w.head - d
	if idx < 0 {
		idx += w.capacity
	}
	return w.pos[idx], true
}

// ByteAtPos returns the byte stored at absolute position p, if still live.
func (w *Window) ByteAtPos(p int64) (byte, bool) {
	if p < 0 || w.next == 0 || p >= w.next {
		return 0, false
	}
	idx := int(p % int64(w.capacity))
	if w.pos[idx] != p { // evicted
		return 0, false
	}
	return w.buf[idx], true
}

// Copy appends the run [d, d+n) of distance-addressed bytes to out. The run
// may be longer than the window: repeated reads emulate the overlap copy
// semantics of expanding a back reference byte by byte.
func (w *Window) Copy(out []byte, d, n int) []byte {
	for k := 0; k < n; k++ {
		// The newest distance-1 byte is the last appended output byte; after
		// each step we push it, so distances walk through freshly copied data.
		c, _ := w.At(d)
		out = append(out, c)
		w.Push(c)
	}
	return out
}
