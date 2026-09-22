// Package frame incrementally decodes a single chunk of a chunked
// transfer-coding stream: a size line, exactly the declared number of
// data bytes, then a CRLF. Input may be split at any byte boundary.
// It depends on hexline for size-line parsing.
package frame

import (
	"errors"
	"fmt"

	"ontology/hexline"
)

// Sentinels for the failures Feed can report; use errors.Is to match.
var (
	ErrLineTooLong = errors.New("frame: chunk-size line exceeds length limit")
	ErrBadCRLF     = errors.New("frame: expected CRLF")
)

// State identifies which part of a chunk the Frame is waiting for.
type State uint8

const (
	StateSizeLine State = iota // accumulating the size line
	StateData                  // consuming chunk data
	StateCR                    // expecting CR after the data
	StateLF                    // expecting LF after CR
	StateDone                  // chunk complete; call Reset for the next one
)

func (s State) String() string {
	switch s {
	case StateSizeLine:
		return "size-line"
	case StateData:
		return "data"
	case StateCR:
		return "cr"
	case StateLF:
		return "lf"
	case StateDone:
		return "done"
	}
	return "unknown"
}

// Error reports a malformed chunk. At is the absolute stream offset
// of the offending byte (computed from the base passed to Feed).
type Error struct {
	Err error // one of the sentinels of this package or of hexline
	At  int64
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v (stream offset %d)", e.Err, e.At)
}

// Unwrap returns the sentinel, so errors.Is works.
func (e *Error) Unwrap() error { return e.Err }

// Frame is an incremental decoder for one chunk. It is not safe for
// concurrent use.
type Frame struct {
	// MaxLine limits the buffered size-line bytes (content plus the
	// CR, excluding the LF); the byte that would exceed it is rejected.
	MaxLine int
	// OnSize is called once the size line is parsed, before any data
	// byte is consumed. A non-nil return aborts Feed with that error.
	// at is the absolute offset of the line's terminating LF.
	OnSize func(size uint64, at int64) error

	line   []byte
	size   uint64
	remain uint64
	state  State
}

// Reset prepares the Frame for the next chunk.
func (f *Frame) Reset() {
	f.line = f.line[:0]
	f.size = 0
	f.remain = 0
	f.state = StateSizeLine
}

// State returns the current frame state.
func (f *Frame) State() State { return f.state }

// Size returns the declared size of the current chunk; meaningful
// once the size line has been parsed.
func (f *Frame) Size() uint64 { return f.size }

// Feed consumes as much of p as possible. base is the absolute stream
// offset of p[0], used for error offsets. emit is called with slices
// of p holding chunk data and must not retain them. It returns the
// number of bytes consumed; on error the offending byte is excluded.
func (f *Frame) Feed(p []byte, base int64, emit func([]byte)) (int, error) {
	n := 0
	for n < len(p) {
		switch f.state {
		case StateSizeLine:
			if p[n] == '\n' {
				if err := f.finishLine(base + int64(n)); err != nil {
					return n, err
				}
				n++
				continue
			}
			if f.MaxLine > 0 && len(f.line) >= f.MaxLine {
				return n, &Error{Err: ErrLineTooLong, At: base + int64(n)}
			}
			f.line = append(f.line, p[n])
			n++
		case StateData:
			take := uint64(len(p) - n)
			if take > f.remain {
				take = f.remain
			}
			emit(p[n : n+int(take)])
			n += int(take)
			f.remain -= take
			if f.remain == 0 {
				f.state = StateCR
			}
		case StateCR:
			if p[n] != '\r' {
				return n, &Error{Err: ErrBadCRLF, At: base + int64(n)}
			}
			n++
			f.state = StateLF
		case StateLF:
			if p[n] != '\n' {
				return n, &Error{Err: ErrBadCRLF, At: base + int64(n)}
			}
			n++
			f.state = StateDone
		case StateDone:
			return n, nil
		}
	}
	return n, nil
}

// finishLine parses the buffered size line; at is the absolute offset
// of its terminating LF.
func (f *Frame) finishLine(at int64) error {
	if len(f.line) == 0 || f.line[len(f.line)-1] != '\r' {
		return &Error{Err: ErrBadCRLF, At: at}
	}
	size, perr := hexline.Parse(f.line[:len(f.line)-1])
	if perr != nil {
		lineStart := at - int64(len(f.line))
		return &Error{Err: perr.Err, At: lineStart + int64(perr.Index)}
	}
	f.line = f.line[:0]
	f.size = size
	f.remain = size
	if f.OnSize != nil {
		if err := f.OnSize(size, at); err != nil {
			return err
		}
	}
	if size == 0 {
		f.state = StateDone
	} else {
		f.state = StateData
	}
	return nil
}
