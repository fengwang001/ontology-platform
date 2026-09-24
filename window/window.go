// Package window implements a fixed-capacity sliding byte window.
package window

// Window is a ring buffer holding the most recent Cap bytes.
type Window struct {
	cap  int
	buf  []byte
	base int // absolute position of buf[0]
	size int // valid bytes (<= cap)
}

// New creates a window with the given positive capacity.
func New(capacity int) *Window {
	return &Window{cap: capacity, buf: make([]byte, capacity)}
}
