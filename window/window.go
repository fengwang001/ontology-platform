// Package window is a fixed-capacity mirrored ring buffer of recent bytes.
package window

import "fmt"

// Window keeps the most recent Cap bytes. The backing array holds two
// identical halves so the last up-to-Cap bytes are always readable as one
// contiguous slice ending at index 2*Cap (when full).
type Window struct {
	buf []byte // length 2*cap
	pos int64  // absolute count of bytes written
	cp  int
}

func New(capacity int) *Window {
	if capacity <= 0 {
		panic(fmt.Sprintf("window: capacity must be > 0, got %d", capacity))
	}
	return &Window{buf: make([]byte, 2*capacity), cp: capacity}
}

func (w *Window) Cap() int { return w.cp }

// Pos is the absolute number of bytes ever written.
func (w *Window) Pos() int64 { return w.pos }

// Len is the number of valid history bytes currently held (<= Cap).
func (w *Window) Len() int {
	n := int(w.pos)
	if n > w.cp {
		return w.cp
	}
	return n
}

func (w *Window) Write(p []byte) {
	for _, b := range p {
		i := int(w.pos) % w.cp
		w.buf[i] = b
		w.buf[w.cp+i] = b
		w.pos++
	}
}

func (w *Window) end() int {
	if int(w.pos) <= w.cp {
		return w.cp + int(w.pos)
	}
	return w.cp + int(w.pos%int64(w.cp))
}

// At returns the byte at distance d (1 == most recent).
func (w *Window) At(d int) byte {
	return w.buf[w.end()-d]
}

// Recent returns a contiguous (ring-aliasing) view of the last l bytes; l<=Len.
func (w *Window) Recent(l int) []byte {
	return w.buf[w.end()-l : w.end()]
}

// CopyMatch appends l bytes at distance d with overlap semantics
// (out[j]=out[j-d]; when d<l the source advances with the output).
// It emits the d real history bytes first, then doubles the run on every
// copy, so O(log l) contiguous copies suffice even for d == 1.
func (w *Window) CopyMatch(dst []byte, d, l int) []byte {
	if d > l {
		return append(dst, w.Recent(l)...)
	}
	start := len(dst)
	dst = append(dst, w.Recent(d)...) // seed: real history
	for len(dst)-start < l {
		have := len(dst) - start
		run := l - have
		if run > have {
			run = have // double the produced prefix each iteration
		}
		dst = append(dst, dst[start:start+run]...)
	}
	return dst
}
