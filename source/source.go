// Package source abstracts the byte origin a range response is assembled
// from. Implementations may legitimately short-read, fail, or even change
// their reported length mid-read; the serve package must cope with all
// three. Flaky exists to inject exactly those behaviours in tests.
package source

import (
	"errors"
	"io"
)

// Source is a random-access byte origin with a known (but possibly
// unstable) length.
type Source interface {
	// Size reports the current total length in bytes.
	Size() int64
	// ReadAt reads up to len(p) bytes starting at off. Returning fewer
	// bytes than requested with a nil error (a short read) is legal.
	// Reaching the end early is reported as io.EOF.
	ReadAt(p []byte, off int64) (int, error)
}

// Bytes is an in-memory Source backed by a byte slice.
type Bytes []byte

// Size returns the slice length.
func (b Bytes) Size() int64 { return int64(len(b)) }

// ReadAt implements io.ReaderAt-style semantics: a full read returns a nil
// error, a partial read at the tail returns io.EOF.
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

// Flaky decorates a Source with injectable faults. The zero value of every
// knob means "disabled".
type Flaky struct {
	Src Source
	// MaxChunk, when > 0, caps the number of bytes each ReadAt returns,
	// forcing callers to loop over short reads.
	MaxChunk int
	// FailOnCall, when > 0, makes the Nth ReadAt call (1-based) fail with
	// Err instead of reading.
	FailOnCall int
	// Err is the error injected on the FailOnCall-th call.
	Err error
	// HasFakeSize makes Size report FakeSize instead of the real length.
	HasFakeSize bool
	FakeSize    int64
	// HasTrunc simulates a resource that shrank mid-read: any read at or
	// past TruncAt hits io.EOF even though Size may claim more.
	HasTrunc bool
	TruncAt  int64

	calls int
}

// Size reports the (possibly faked) length.
func (f *Flaky) Size() int64 {
	if f.HasFakeSize {
		return f.FakeSize
	}
	return f.Src.Size()
}

// ReadAt applies the configured faults around the wrapped source.
func (f *Flaky) ReadAt(p []byte, off int64) (int, error) {
	f.calls++
	if f.FailOnCall > 0 && f.calls == f.FailOnCall {
		return 0, f.Err
	}
	if f.HasTrunc && off >= f.TruncAt {
		return 0, io.EOF
	}
	if f.MaxChunk > 0 && len(p) > f.MaxChunk {
		p = p[:f.MaxChunk]
	}
	n, err := f.Src.ReadAt(p, off)
	if f.HasTrunc && off+int64(n) > f.TruncAt {
		n = int(f.TruncAt - off)
		err = io.EOF
	}
	return n, err
}
