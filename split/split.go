// Package split cuts a byte stream into content-defined chunks.
//
// It implements scheme (乙) from DESIGN.md: the rolling hash is fed every byte
// continuously across chunk boundaries; boundaries are merely vetoed while the
// current chunk is shorter than Min.
package split

import (
	"errors"

	"ontology/roll"
)

var (
	// ErrMinMax means Min > Max.
	ErrMinMax = errors.New("split: min must be <= max")
	// ErrWindow means the rolling window is 0 or longer than Min.
	ErrWindow = errors.New("split: window must be > 0 and <= min")
)

// Config configures a Chunker.
type Config struct {
	Window int    // rolling-hash window length w
	Min    int    // minimum chunk length
	Max    int    // maximum chunk length
	Mask   uint32 // boundary predicate: hash & Mask == 0; 0 defaults to 0x1f
}

// Chunker is a streaming content-defined chunker.
type Chunker struct {
	min, max int
	mask     uint32
	h        *roll.Hasher
	buf      []byte // bytes of the chunk currently open
	off      int64  // stream offset of the first buffered byte
}

// State is an opaque snapshot used to roll back a rejected feed.
type State struct {
	buf []byte
	off int64
	h   *roll.Hasher
}

// New constructs a chunker and validates its parameters.
func New(c Config) (*Chunker, error) {
	if c.Min > c.Max {
		return nil, ErrMinMax
	}
	if c.Window <= 0 || c.Window > c.Min {
		return nil, ErrWindow
	}
	h, err := roll.New(c.Window)
	if err != nil {
		return nil, err
	}
	mask := c.Mask
	if mask == 0 {
		mask = 0x1f
	}
	return &Chunker{min: c.Min, max: c.Max, mask: mask, h: h}, nil
}

// Snapshot returns the current state.
func (c *Chunker) Snapshot() State {
	return State{buf: append([]byte(nil), c.buf...), off: c.off, h: c.h.Clone()}
}

// Restore restores a snapshot taken with Snapshot.
func (c *Chunker) Restore(s State) {
	c.buf = append(c.buf[:0], s.buf...)
	c.off = s.off
	c.h = s.h.Clone()
}

func (c *Chunker) emit() Chunk {
	data := append([]byte(nil), c.buf...)
	ch := Chunk{Start: c.off, Data: data}
	c.off += int64(len(data))
	c.buf = c.buf[:0]
	return ch
}

// Feed appends p and returns every chunk that becomes complete.
// Emitted chunks are independent copies; p may be reused by the caller.
func (c *Chunker) Feed(p []byte) []Chunk {
	var out []Chunk
	for _, b := range p {
		var atBoundary bool
		if c.h.Full() {
			c.h.Push(b)
			n := len(c.buf) + 1
			atBoundary = n >= c.min && (n >= c.max || c.h.Sum32()&c.mask == 0)
		} else {
			c.h.Write(b)
		}
		c.buf = append(c.buf, b)
		if atBoundary {
			out = append(out, c.emit())
		}
	}
	return out
}

// Tail returns the bytes that remain after the last emitted boundary.
// It does not decide min/max merging; the caller does that once per stream.
func (c *Chunker) Tail() []byte {
	return append([]byte(nil), c.buf...)
}

// Chunk is one piece of the stream with its absolute start offset.
type Chunk struct {
	Start int64
	Data  []byte
}

// End is the exclusive end offset.
func (ch Chunk) End() int64 { return ch.Start + int64(len(ch.Data)) }
