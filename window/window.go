// Package window is a fixed-capacity circular byte history.
package window

import "fmt"

// Window keeps the most recent Cap bytes ever written.
type Window struct {
	buf  []byte
	head int // index where the next byte is written
	size int // number of valid bytes (<= cap)
}

// New creates a window. capacity must be positive.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("window: capacity must be positive, got %d", capacity)
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently held.
func (w *Window) Len() int { return w.size }

// Add appends one byte, evicting the oldest when full.
func (w *Window) Add(b byte) {
	w.buf[w.head] = b
	w.head++
	if w.head == len(w.buf) {
		w.head = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// Write appends p.
func (w *Window) Write(p []byte) {
	for _, b := range p {
		w.Add(b)
	}
}

// At returns the byte at distance dist from the newest (1 = most recent).
func (w *Window) At(dist int) byte {
	if dist < 1 || dist > w.size {
		panic("window: distance out of range")
	}
	i := w.head - dist
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

// Tail copies up to n most recent bytes in chronological order.
func (w *Window) Tail(n int) []byte {
	if n > w.size {
		n = w.size
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
	out[i] = w.At(n - i)
	}
	return out
}
