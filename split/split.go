// Package split cuts a byte stream into content-defined chunks.
package split

import (
	"errors"

	"ontology/roll"
)

var (
	// ErrBadBound is returned when min > max.
	ErrBadBound = errors.New("split: min must be <= max")
	// ErrBadWindow is returned when window is zero or greater than min.
	ErrBadWindow = errors.New("split: window must be in [1,min]")
)

// Commit is invoked once per finalized chunk with its stream offsets and data.
// A non-nil error rejects the boundary: the bytes remain part of the current
// chunk and no state is committed.
type Commit func(start, end int, data []byte) error

// Splitter is a streaming content-defined chunker.
type Splitter struct {
	min, max int
	mask     uint64
	rh       *roll.Hash
	commit   Commit
	start    int
	off      int
	buf      []byte
}

// Config configures a Splitter.
type Config struct {
	Window, Min, Max int
	Mask             uint64 // boundary when rollingHash & Mask == 0
	OnCommit         Commit
}

// New validates parameters and builds a Splitter.
func New(c Config) (*Splitter, error) {
	if c.Min > c.Max {
		return nil, ErrBadBound
}
	if c.Window <= 0 || c.Window > c.Min {
		return nil, ErrBadWindow
}
	rh, err := roll.New(c.Window)
	if err != nil {
		return nil, ErrBadWindow
	}
	return &Splitter{min: c.Min, max: c.Max, mask: c.Mask, rh: rh, commit: c.OnCommit}, nil
}

// Write appends bytes and emits every finalized chunk.
func (s *Splitter) Write(p []byte) error {
	for _, b := range p {
		chunkLen := s.off - s.start
		h, full := s.rh.Push(b)
		s.buf = append(s.buf, b)
		s.off++
		if !full || chunkLen < s.min {
			continue
		}
		hit := h&s.mask == 0
		if !hit && chunkLen < s.max {
			continue
		}
		data := s.buf
		if err := s.commit(s.start, s.off, data); err != nil {
			s.off--
			s.buf = s.buf[:len(s.buf)-1]
			return err
		}
		s.start = s.off
		s.buf = nil
		s.rh.Reset()
	}
	return nil
}

// Flush finalizes the trailing bytes (possibly shorter than min, or empty)
// as one chunk. It reports whether a chunk was emitted.
func (s *Splitter) Flush() (bool, error) {
	if err := s.commit(s.start, s.off, s.buf); err != nil {
		return false, err
	}
	emitted := s.off > s.start || len(s.buf) == 0
	s.start = s.off
	s.buf = nil
	s.rh.Reset()
	return emitted, nil
}
