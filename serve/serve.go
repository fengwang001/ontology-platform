// Package serve assembles multi-range byte responses: it parses a Range
// header, normalizes the ranges, reads the bytes from a possibly faulty
// source, frames them (multipart/byteranges when more than one range
// survives), and supports resumable writes to short-writing destinations.
//
// Concurrency: an Assembler is stateless and safe for concurrent use, and
// independent Response values may be used from different goroutines. A
// single Response is NOT safe for concurrent writes: its write cursor is
// advanced without locking, so one Response must be written from one
// goroutine at a time.
package serve

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/source"
)

// Distinguishable failure modes of Build, testable with errors.Is.
var (
	// ErrTooManyRanges rejects a header with more range specs than allowed.
	ErrTooManyRanges = errors.New("serve: too many ranges")
	// ErrTooLarge rejects a response whose assembled size exceeds the limit.
	ErrTooLarge = errors.New("serve: response exceeds byte limit")
	// ErrShortSource reports EOF from the source before a range was filled.
	ErrShortSource = errors.New("serve: source ended before a range was fully read")
)

// Limits caps resource usage. Zero fields get the documented defaults.
type Limits struct {
	MaxRanges      int   // max range specs per request (default 64)
	MaxBytes       int64 // max assembled response bytes (default 32 MiB)
	MaxBoundaryTry int   // max boundary candidates to try (default 8)
}

const (
	defaultMaxRanges = 64
	defaultMaxBytes  = 32 << 20
	defaultMaxTries  = 8
)

func (l Limits) withDefaults() Limits {
	if l.MaxRanges <= 0 {
		l.MaxRanges = defaultMaxRanges
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = defaultMaxBytes
	}
	if l.MaxBoundaryTry <= 0 {
		l.MaxBoundaryTry = defaultMaxTries
	}
	return l
}

// Assembler builds range responses from a Source. It keeps no mutable
// state, so a rejected Build never changes anything.
type Assembler struct {
	Src    source.Source
	Limits Limits
	// Rand supplies boundary randomness; nil means crypto/rand.Reader.
	Rand io.Reader
}

// Build parses header, normalizes the ranges against the source size, reads
// the content (completing short reads), and assembles the response body.
// A single surviving range is served as raw bytes; two or more are framed
// as multipart/byteranges.
func (a *Assembler) Build(header string) (*Response, error) {
	lim := a.Limits.withDefaults()
	specs, err := rangespec.Parse(header)
	if err != nil {
		return nil, err
	}
	if len(specs) > lim.MaxRanges {
		return nil, fmt.Errorf("%w: got %d, limit %d", ErrTooManyRanges, len(specs), lim.MaxRanges)
	}
	total := a.Src.Size()
	ranges, err := coalesce.Normalize(specs, total)
	if err != nil {
		return nil, err
	}
	var contentBytes int64
	for _, r := range ranges {
		contentBytes += r.Len()
	}
	multi := len(ranges) > 1
	size := contentBytes
	if multi {
		size += multipart.FramingSize(multipart.BoundaryLen, ranges, total)
	}
	if size > lim.MaxBytes {
		return nil, fmt.Errorf("%w: need %d, limit %d", ErrTooLarge, size, lim.MaxBytes)
	}
	content := make([]byte, contentBytes)
	off := int64(0)
	for _, r := range ranges {
		if err := readFull(a.Src, content[off:off+r.Len()], r.First); err != nil {
			return nil, err
		}
		off += r.Len()
	}
	body := content
	if multi {
		rng := a.Rand
		if rng == nil {
			rng = crand.Reader
		}
		boundary, err := multipart.ChooseBoundary(content, lim.MaxBoundaryTry, rng)
		if err != nil {
			return nil, err
		}
		body = multipart.Assemble(boundary, ranges, total, content)
	}
	return &Response{body: body, ranges: ranges, total: total, multi: multi}, nil
}

// readFull fills p from src starting at off, looping over short reads. EOF
// before p is full is ErrShortSource, never a silent truncation.
func readFull(src source.Source, p []byte, off int64) error {
	for len(p) > 0 {
		n, err := src.ReadAt(p, off)
		if n > 0 {
			p = p[n:]
			off += int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(p) == 0 {
					return nil
				}
				return ErrShortSource
			}
			return err
		}
		if n == 0 {
			return ErrShortSource // no progress and no error: broken source
		}
	}
	return nil
}
