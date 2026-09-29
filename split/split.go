// Package split cuts a byte stream at content-defined boundaries.
package split

import (
	"errors"

	"ontology/roll"
)

var (
	// ErrMinMax is returned when min is not within [1, max].
	ErrMinMax = errors.New("split: require 1 <= min <= max")
	// ErrWindow is returned when the window is 0 or larger than min.
	ErrWindow = errors.New("split: require 1 <= window <= min")
)

// Chunk is one emitted chunk with stream-absolute offsets.
type Chunk struct {
	Start  int64
	End    int64 // exclusive
	Data   []byte
	Forced bool // cut by the max-length rule rather than a content boundary
}

// Config configures a Splitter.
type Config struct {
	Window int
	Min    int
	Max    int
	Mask   uint64 // boundary when (hash & Mask) == 0
}

// Splitter is a stateful, write-size-independent chunker.
type Splitter struct {
	cfg  Config
	hash *roll.Hasher

	streamPos  int64
	chunkStart int64
	buf        []byte
	closed     []Chunk
}

// New validates the configuration and builds a splitter.
func New(cfg Config) (*Splitter, error) {
	if cfg.Min < 1 || cfg.Min > cfg.Max {
		return nil, ErrMinMax
	}
	if cfg.Window <= 0 || cfg.Window > cfg.Min {
		return nil, ErrWindow
	}
	h, err := roll.New(cfg.Window)
	if err != nil {
		return nil, ErrWindow
	}
	if cfg.Mask == 0 {
		cfg.Mask = ^uint64(0)
	}
	return &Splitter{cfg: cfg, hash: h}, nil
}

// Write appends bytes; chunking is independent of how writes are split.
func (s *Splitter) Write(p []byte) {
	for _, b := range p {
		s.streamPos++
		off := len(s.buf) // index the new byte gets within the current chunk
		s.buf = append(s.buf, b)
		if off+1 < s.cfg.Min {
			continue // prefix is never fed to the hash
		}
		s.hash.Push(b)
		length := off + 1
		contentCut := s.hash.Full() && s.hash.Sum()&s.cfg.Mask == 0
		if length >= s.cfg.Max || (length >= s.cfg.Min && contentCut) {
			s.emit(length >= s.cfg.Max && !contentCut)
		}
	}
}

func (s *Splitter) emit(forced bool) {
	data := append([]byte(nil), s.buf...)
	s.buf = nil
	c := Chunk{Start: s.chunkStart, End: s.chunkStart + int64(len(data)), Data: data, Forced: forced}
	s.closed = append(s.closed, c)
	s.chunkStart = c.End
	s.hash.Reset()
}

// Flush finalizes the trailing bytes and returns the full chunk sequence.
func (s *Splitter) Flush() []Chunk {
	if len(s.buf) > 0 {
		if len(s.closed) > 0 { // merge into the previous chunk
			last := &s.closed[len(s.closed)-1]
			last.Data = append(last.Data, s.buf...)
			last.End += int64(len(s.buf))
			last.Forced = false
			s.buf = nil
		} else {
			s.emit(false)
		}
	}
	out := s.closed
	s.closed = nil
	s.chunkStart = 0
	s.streamPos = 0
	return out
}
