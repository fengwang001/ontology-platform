package window

import "errors"

var ErrConfig = errors.New("window: capacity must be positive")

// Window is a fixed-capacity ring of the most recently appended bytes.
// Distance is 1-based: distance 1 is the most recently appended byte.
type Window struct {
	buf  []byte
	head int
	size int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

func (w *Window) Cap() int  { return len(w.buf) }
func (w *Window) Len() int  { return w.size }

func (w *Window) Reset() {
	w.head, w.size = 0, 0
}

func (w *Window) Append(b byte) {
	w.buf[w.head] = b
	w.head++
	if w.head == len(w.buf) {
		w.head = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

func (w *Window) AppendBytes(p []byte) {
	for _, b := range p {
		w.Append(b)
	}
}

// Byte returns the byte at the given 1-based distance.
func (w *Window) Byte(dist int) byte {
	i := w.head - dist
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

// Copy appends length bytes copied from a 1-based distance into dst,
// correctly handling overlapping references (length > dist): source bytes
// are read from dst, so each copied byte immediately feeds later copies.
func (w *Window) Copy(dst []byte, dist, length int) []byte {
	start := len(dst)
	for len(dst) < start+length {
		s := len(dst) - dist
		n := dist
	if rem := start + length - len(dst); n > rem {
			n = rem
		}
		chunk := dst[s : s+n]
		dst = append(dst, chunk...)
		w.AppendBytes(chunk)
	}
	return dst
}
