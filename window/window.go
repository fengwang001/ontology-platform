// Package window is a fixed-capacity sliding window over recent bytes.
package window

// Window is a ring buffer holding the most recent Cap bytes.
// It is not safe for concurrent use.
type Window struct {
	buf   []byte
	cap   int
	start int // index of the oldest byte when full
	size  int
	base  int // absolute position of the oldest live byte
}

func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity), cap: capacity}
}

func (w *Window) Cap() int { return w.cap }

// Len is the number of live bytes in the window.
func (w *Window) Len() int { return w.size }

// Push appends bytes, dropping the oldest when capacity is exceeded.
func (w *Window) Push(p []byte) {
	if len(p) >= w.cap {
		copy(w.buf, p[len(p)-w.cap:])
		w.start, w.size = 0, w.cap
		w.base += len(p) - w.cap
		return
	}
	for _, c := range p {
		w.buf[(w.start+w.size)%w.cap] = c
		if w.size == w.cap {
			w.start = (w.start + 1) % w.cap
			w.base++
			continue
		}
		w.size++
	}
}

// Base is the absolute position of the oldest live byte.
func (w *Window) Base() int { return w.base }

// Live reports whether absolute position pos is held in the window.
func (w *Window) Live(pos int) bool {
	return pos >= w.base && pos < w.base+w.size
}

// ByteAt returns the byte at absolute stream position pos (0 based).
func (w *Window) ByteAt(pos int) byte {
	if !w.Live(pos) {
		panic("window: position out of range")
	}
	return w.buf[(w.start+(pos-w.base))%w.cap]
}

// AppendAt appends n bytes starting at absolute position pos.
func (w *Window) AppendAt(dst []byte, pos, n int) []byte {
	for i := 0; i < n; i++ {
		dst = append(dst, w.ByteAt(pos+i))
	}
	return dst
}
