// Package frame decodes a single chunk: a size line, exactly the
// declared number of data bytes, and a terminating CRLF.
//
// Input may be fragmented at any byte position; a Frame resumes where
// it stopped on the next Write. It depends on hexline for the size
// line and knows nothing about trailers or whole-message state.
// A Frame is not safe for concurrent use.
package frame

import (
	"errors"

	"ontology/hexline"
)

// ErrMissingCRLF indicates the bytes following chunk data were not
// the required CRLF (wrong byte, single LF, or extra data byte).
var ErrMissingCRLF = errors.New("frame: chunk data not followed by CRLF")

// State identifies where a Frame stands within one chunk.
type State int

const (
	// StateSizeLine means the size line is still being read.
	StateSizeLine State = iota
	// StateData means chunk data bytes are being read.
	StateData
	// StateExpectCR means the frame waits for the CR after the data.
	StateExpectCR
	// StateExpectLF means the CR was consumed and the LF is pending.
	StateExpectLF
	// StateDone means the frame is complete.
	StateDone
)

// Frame is a streaming decoder for one chunk.
type Frame struct {
	scanner *hexline.Scanner
	state   State
	size    uint64
	remain  uint64
}

// New returns a Frame whose size lines may be at most maxLineLen
// bytes long (excluding the CRLF).
func New(maxLineLen int) *Frame {
	return &Frame{scanner: hexline.NewScanner(maxLineLen), state: StateSizeLine}
}

// State reports the frame's current state.
func (f *Frame) State() State {
	return f.state
}

// Size returns the declared chunk size and whether it is known yet
// (i.e. the size line has been fully parsed).
func (f *Frame) Size() (uint64, bool) {
	if f.state == StateSizeLine {
		return 0, false
	}
	return f.size, true
}

// HalfCR reports whether input stopped right after a CR, with the
// matching LF still pending (either in the size line or after data).
func (f *Frame) HalfCR() bool {
	return f.state == StateExpectLF ||
		(f.state == StateSizeLine && f.scanner.PendingCR())
}

// Write consumes p, appending chunk data bytes to dst and returning
// the extended slice. It reports how many bytes of p were consumed
// and whether the frame completed.
//
// check is invoked once, immediately after the size line is parsed
// and before any data byte is consumed; a non-nil result aborts the
// frame with that error. On error the consumed count includes the
// offending byte. A zero-size frame completes right after its size
// line (no data, no trailing CRLF).
func (f *Frame) Write(p []byte, dst []byte, check func(size uint64) error) ([]byte, int, bool, error) {
	consumed := 0
	for consumed < len(p) {
		switch f.state {
		case StateSizeLine:
			line, n, complete, err := f.scanner.Write(p[consumed:])
			consumed += n
			if err != nil {
				return dst, consumed, false, err
			}
			if !complete {
				return dst, consumed, false, nil
			}
			size, err := hexline.Parse(line)
			if err != nil {
				return dst, consumed, false, err
			}
			f.size, f.remain = size, size
			if check != nil {
				if err := check(size); err != nil {
					return dst, consumed, false, err
				}
			}
			if size == 0 {
				f.state = StateDone
				return dst, consumed, true, nil
			}
			f.state = StateData
		case StateData:
			n := int(f.remain)
			if left := len(p) - consumed; left < n {
				n = left
			}
			dst = append(dst, p[consumed:consumed+n]...)
			consumed += n
			f.remain -= uint64(n)
			if f.remain == 0 {
				f.state = StateExpectCR
			}
		case StateExpectCR:
			if p[consumed] != '\r' {
				return dst, consumed + 1, false, ErrMissingCRLF
			}
			consumed++
			f.state = StateExpectLF
		case StateExpectLF:
			if p[consumed] != '\n' {
				return dst, consumed + 1, false, ErrMissingCRLF
			}
			consumed++
			f.state = StateDone
			return dst, consumed, true, nil
		default: // StateDone
			return dst, consumed, true, nil
		}
	}
	return dst, consumed, f.state == StateDone, nil
}
