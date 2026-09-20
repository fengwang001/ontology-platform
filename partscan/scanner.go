// Package partscan implements a delimiter-driven incremental part
// scanner. A stream starts with "--<boundary>\r\n", parts are separated
// by "\r\n--<boundary>\r\n" and the stream ends with
// "\r\n--<boundary>--". Bytes may be fed in arbitrarily sized chunks;
// the scanner emits each completed part exactly once.
package partscan

import "errors"

var (
	// ErrNoPreamble is returned when the stream does not start with
	// "--<boundary>\r\n". The scanner enters a terminal failed state.
	ErrNoPreamble = errors.New("partscan: stream does not start with --<boundary>\\r\\n")
	// ErrIncomplete is returned by Close when the closing delimiter
	// has not been seen yet.
	ErrIncomplete = errors.New("partscan: stream closed before closing delimiter")
	// ErrAfterClose is returned when non-empty data is fed after the
	// closing delimiter has been seen.
	ErrAfterClose = errors.New("partscan: data fed after closing delimiter")
)

type state int

const (
	statePreamble state = iota
	stateParts
	stateDone
	stateFailed
)

// Scanner incrementally splits a delimiter-framed byte stream into
// parts. The zero value is not usable; use New.
type Scanner struct {
	preamble []byte // "--<boundary>\r\n"
	marker   []byte // "\r\n--<boundary>"
	state    state
	err      error  // sticky terminal error (stateFailed)
	buf      []byte // unprocessed bytes, bounded by len(marker)+1
	part     []byte // accumulated content of the current part
}

// New returns a Scanner for the given boundary.
func New(boundary string) *Scanner {
	return &Scanner{
		preamble: []byte("--" + boundary + "\r\n"),
		marker:   []byte("\r\n--" + boundary),
	}
}

// Done reports whether the closing delimiter has been seen.
func (s *Scanner) Done() bool { return s.state == stateDone }

// Pending returns the number of bytes currently held for the
// not-yet-completed part.
func (s *Scanner) Pending() int { return len(s.buf) + len(s.part) }

// Close signals end of stream. It returns ErrIncomplete if the closing
// delimiter has not been seen. Close is idempotent.
func (s *Scanner) Close() error {
	switch s.state {
	case stateDone:
		return nil
	case stateFailed:
		return s.err
	default:
		return ErrIncomplete
	}
}

func (s *Scanner) fail(err error) {
	s.state = stateFailed
	s.err = err
	s.buf = nil
	s.part = nil
}
