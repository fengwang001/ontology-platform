// Package window implements a fixed-capacity sliding window over a byte
// stream: a ring buffer that can retrieve recently produced bytes by
// distance (1 = most recent byte).
package window

// Window is a fixed-size ring buffer. The zero value is not usable;
// construct with New.
type Window struct {
	buf  []byte
	mask uint64
	size uint64 // logical capacity (power of two)
	len  uint64 // bytes currently stored (<= size)
	pos  uint64 // total bytes ever pushed; next write slot index = pos & mask
}

// New returns a window whose capacity is the smallest power of two that
// is >= capBytes. capBytes must be positive.
func New(capBytes int) *Window {
	if capBytes <= 0 {
		panic("window: capacity must be positive")
	}
	size := uint64(1)
	for size < uint64(capBytes) {
		size <<= 1
	}
	return &Window{buf: make([]byte, size), mask: size - 1, size: size}
}

// Cap reports the logical capacity.
func (w *Window) Cap() uint64 { return w.size }

// Len reports the number of bytes currently available.
func (w *Window) Len() uint64 { return w.len }

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(b byte) {
	w.buf[w.pos&w.mask] = b
	w.pos++
	if w.len < w.size {
		w.len++
	}
}

// Write appends p to the window.
func (w *Window) Write(p []byte) {
	for _, b := range p {
		w.Push(b)
	}
}

// At returns the byte at distance d (1 = most recently pushed byte).
// It panics when d == 0 or d > Len().
func (w *Window) At(d uint64) byte {
	if d == 0 || d > w.len {
		panic("window: distance out of range")
	}
	return w.buf[(w.pos-d)&w.mask]
}

// CopyPrefix seeds the window with a dictionary that is conceptually the
// prefix of the stream (the dictionary's last byte is the most recent
// byte). Only the last Cap() bytes of p are retained.
func (w *Window) CopyPrefix(p []byte) {
	if uint64(len(p)) > w.size {
		p = p[uint64(len(p))-w.size:]
	}
	for _, b := range p {
		w.Push(b)
	}
}

// CopyReplay appends length bytes copied from distance d into out and
// pushes them into the window. It correctly handles d < length (overlap):
// each round copies from bytes already present, equivalent to byte-by-byte
// out[i] = out[i-d].
func (w *Window) CopyReplay(d, length uint64, out []byte) []byte {
	if d == 0 || d > w.len {
		panic("window: distance out of range")
	}
	start := uint64(len(out))
	out = append(out, make([]byte, length)...)
	remaining := length
	dst := out[start:]
	for remaining > 0 {
		n := remaining
		if n > d {
			n = d
		}
		srcStart := uint64(len(out)) - d
		copy(dst[:n], out[srcStart:srcStart+n])
		for i := uint64(0); i < n; i++ {
			w.Push(dst[i])
		}
		dst = dst[n:]
		remaining -= n
	}
	return out
}
