// Package serve assembles multi-range byte responses: it parses a Range
// header, normalizes the ranges, reads the bytes from a (possibly
// short-reading or failing) source, frames them as multipart/byteranges
// when needed, and supports resumable writes to short-writing sinks.
//
// Concurrency: distinct Assembler values are fully independent and safe
// to use from separate goroutines. A single Assembler is NOT safe for
// concurrent use: WriteTo advances shared state, so one assembler must
// be driven by one goroutine at a time (queries included).
package serve

import (
	"errors"
	"fmt"
	"io"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/source"
)

// Distinguishable failure modes, testable with errors.Is.
var (
	// ErrTooManyRanges: the header listed more ranges than MaxRanges.
	ErrTooManyRanges = errors.New("serve: range count limit exceeded")
	// ErrResponseTooLarge: the assembled body exceeds MaxBytes.
	ErrResponseTooLarge = errors.New("serve: response byte limit exceeded")
	// ErrBoundaryExhausted: no conflict-free boundary within the retries.
	ErrBoundaryExhausted = errors.New("serve: boundary retry limit exhausted")
	// ErrShortData: the source hit EOF before a range was fully read.
	ErrShortData = errors.New("serve: source ended before range was filled")
	// ErrNotAssembled: WriteTo called before a successful Assemble.
	ErrNotAssembled = errors.New("serve: not assembled yet")
)

// Config tunes the resource limits of an Assembler. Non-positive values
// are replaced by defaults.
type Config struct {
	MaxRanges        int   // limit on range specs per header
	MaxBytes         int64 // limit on total assembled response bytes
	MaxBoundaryTries int   // limit on boundary generation attempts
	ContentType      string
	// Rand supplies boundary randomness; nil means crypto/rand.
	Rand io.Reader
}

func (c Config) withDefaults() Config {
	if c.MaxRanges <= 0 {
		c.MaxRanges = 64
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 16 << 20
	}
	if c.MaxBoundaryTries <= 0 {
		c.MaxBoundaryTries = 8
	}
	if c.ContentType == "" {
		c.ContentType = "application/octet-stream"
	}
	return c
}

// Assembler holds the state of one range response. The zero-use pattern
// is: NewAssembler, Assemble, then WriteTo (possibly repeatedly, resuming
// after short writes or errors).
type Assembler struct {
	cfg Config
	src source.Source

	ranges    []coalesce.Range
	body      []byte
	written   int64
	multipart bool
	boundary  string
	assembled bool
}

// NewAssembler creates an assembler reading from src.
func NewAssembler(cfg Config, src source.Source) *Assembler {
	return &Assembler{cfg: cfg.withDefaults(), src: src}
}

// Assemble parses rangeHeader and builds the full response body in memory.
// On any error the assembler keeps its previous state untouched.
func (a *Assembler) Assemble(rangeHeader string) error {
	specs, err := rangespec.Parse(rangeHeader)
	if err != nil {
		return err
	}
	if len(specs) > a.cfg.MaxRanges {
		return fmt.Errorf("%w: got %d, limit %d", ErrTooManyRanges, len(specs), a.cfg.MaxRanges)
	}
	size := a.src.Size()
	rs, err := coalesce.Normalize(specs, size)
	if err != nil {
		return err
	}
	payload, err := readAll(a.src, rs)
	if err != nil {
		return err
	}
	body := payload
	isMulti := false
	boundary := ""
	if len(rs) > 1 {
		isMulti = true
		boundary, err = a.pickBoundary(payload)
		if err != nil {
			return err
		}
		body = multipart.Build(boundary, rs, payload, size, a.cfg.ContentType)
	}
	if int64(len(body)) > a.cfg.MaxBytes {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrResponseTooLarge, len(body), a.cfg.MaxBytes)
	}
	a.ranges = rs
	a.body = body
	a.written = 0
	a.multipart = isMulti
	a.boundary = boundary
	a.assembled = true
	return nil
}

// pickBoundary retries random boundaries until one does not collide with
// the payload, giving up after MaxBoundaryTries attempts.
func (a *Assembler) pickBoundary(payload []byte) (string, error) {
	for try := 0; try < a.cfg.MaxBoundaryTries; try++ {
		b, err := multipart.NewBoundary(a.cfg.Rand)
		if err != nil {
			return "", err
		}
		if !multipart.Conflicts(b, payload) {
			return b, nil
		}
	}
	return "", fmt.Errorf("%w: %d tries", ErrBoundaryExhausted, a.cfg.MaxBoundaryTries)
}

// readAll reads every range fully, concatenating the bytes in range order.
func readAll(src source.Source, rs []coalesce.Range) ([]byte, error) {
	var total int64
	for _, r := range rs {
		total += r.Len()
	}
	out := make([]byte, total)
	pos := int64(0)
	for _, r := range rs {
		n := r.Len()
		if err := readFull(src, out[pos:pos+n], r.From); err != nil {
			return nil, err
		}
		pos += n
	}
	return out, nil
}

// readFull loops over short reads until dst is filled. io.EOF (or a
// zero-byte read making no progress) before dst is full is ErrShortData.
func readFull(src source.Source, dst []byte, off int64) error {
	got := 0
	for got < len(dst) {
		n, err := src.ReadAt(dst[got:], off+int64(got))
		got += n
		if err != nil {
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("%w: at offset %d, %d bytes short",
					ErrShortData, off+int64(got), len(dst)-got)
			}
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: no progress at offset %d", ErrShortData, off+int64(got))
		}
	}
	return nil
}
