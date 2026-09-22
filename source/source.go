// Package source abstracts the byte source a range response is assembled
// from, plus composable wrappers that inject the failure modes an assembler
// must survive: short reads, read errors, and mid-read length changes.
package source

import (
	"errors"
	"io"
)

// Source is a random-access byte source with a known total size.
// ReadAt follows io.ReaderAt conventions: it may return fewer bytes than
// requested (short read) together with a nil error.
type Source interface {
	io.ReaderAt
	Size() int64
}

// Bytes serves an in-memory byte slice as a Source.
type Bytes []byte

// ReadAt implements Source.
func (b Bytes) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("source: negative offset")
	}
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Size implements Source.
func (b Bytes) Size() int64 { return int64(len(b)) }

// Short caps every read at Max bytes, injecting short reads.
type Short struct {
	Src Source
	Max int
}

// ReadAt implements Source.
func (s Short) ReadAt(p []byte, off int64) (int, error) {
	if s.Max > 0 && len(p) > s.Max {
		p = p[:s.Max]
	}
	return s.Src.ReadAt(p, off)
}

// Size implements Source.
func (s Short) Size() int64 { return s.Src.Size() }

// FailAt returns Err for any read that starts at or beyond offset At.
type FailAt struct {
	Src Source
	At  int64
	Err error
}

// ReadAt implements Source.
func (f FailAt) ReadAt(p []byte, off int64) (int, error) {
	if f.Err != nil && off >= f.At {
		return 0, f.Err
	}
	return f.Src.ReadAt(p, off)
}

// Size implements Source.
func (f FailAt) Size() int64 { return f.Src.Size() }

// ShrinkAfter simulates a source whose length changes mid-read: after N
// ReadAt calls it pretends to be only NewSize bytes long, so later reads hit
// EOF early. Size still delegates to the wrapped source, mimicking a caller
// that already snapshotted the old length.
type ShrinkAfter struct {
	Src     Source
	N       int
	NewSize int64
	calls   int
}

// ReadAt implements Source.
func (s *ShrinkAfter) ReadAt(p []byte, off int64) (int, error) {
	s.calls++
	if s.calls <= s.N {
		return s.Src.ReadAt(p, off)
	}
	if off >= s.NewSize {
		return 0, io.EOF
	}
	truncated := false
	if remain := s.NewSize - off; remain < int64(len(p)) {
		p = p[:remain]
		truncated = true
	}
	n, err := s.Src.ReadAt(p, off)
	if err == nil && truncated {
		err = io.EOF
	}
	return n, err
}

// Size implements Source.
func (s *ShrinkAfter) Size() int64 { return s.Src.Size() }
