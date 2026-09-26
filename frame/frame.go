// Package frame encodes/decodes '\n'-delimited, backslash-escaped frames.
package frame

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"ontology/esc"
)

// ErrMissingTerminator: stream ends with frame data but no final '\n'.
var ErrMissingTerminator = errors.New("frame: missing frame terminator")

// scanStats holds unexported Decode counters; only verdicts cross boundary.
type scanStats struct {
	examined atomic.Int64
	reread   atomic.Int64 // always 0 by construction
}

var lastDecode scanStats

// next scans one frame: payload and bytes consumed incl. delimiter; st counts
// each consumed byte exactly once when non-nil.
func next(data []byte, st *scanStats) (out []byte, n int, err error) {
	add := func(k int) {
		if st != nil {
			st.examined.Add(int64(k))
		}
	}
	out = make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		switch b := data[i]; {
		case b == '\n':
			add(1)
			return out, i + 1, nil
		case b != '\\':
			add(1)
			out = append(out, b)
			i++
		case i+1 >= len(data): // '\' at end of stream
			add(1)
			return nil, 0, esc.ErrDanglingEscape
		default:
			add(2) // introducer and target examined together, exactly once
			switch data[i+1] {
			case '\\':
				out = append(out, '\\')
			case 'n':
				out = append(out, '\n')
			case '\n': // '\' directly before a frame boundary
				return nil, 0, esc.ErrDanglingEscape
			default:
				return nil, 0, esc.ErrIllegalEscape
			}
			i += 2
		}
	}
	if len(data) == 0 {
		return nil, 0, io.EOF
	}
	return nil, 0, ErrMissingTerminator
}

// Encode joins frames with one bare '\n' after each escaped payload.
func Encode(frames [][]byte) []byte {
	out := make([]byte, 0)
	for _, f := range frames {
		out = append(append(out, esc.Escape(f)...), '\n')
	}
	return out
}

// Decode decodes the whole stream in one pass; a bad frame rejects all (nil).
func Decode(data []byte) ([][]byte, error) {
	lastDecode.examined.Store(0)
	lastDecode.reread.Store(0)
	frames := make([][]byte, 0)
	for pos := 0; pos < len(data); {
		f, n, err := next(data[pos:], &lastDecode)
		if err != nil {
			return nil, err
		}
		frames = append(frames, f)
		pos += n
	}
	return frames, nil
}

// Reader is a cursor-based single-pass decoder over a fixed byte slice.
type Reader struct {
	data []byte
	pos  int
}

// NewReader starts a Reader at the beginning of data.
func NewReader(data []byte) *Reader { return &Reader{data: data} }

// Pos returns the stream offset; it moves only after a successful frame.
func (r *Reader) Pos() int { return r.pos }

// NextFrame returns the next unescaped frame or io.EOF at clean end. On error
// the cursor is frozen, so the Reader stays usable at the same position.
func (r *Reader) NextFrame() ([]byte, error) {
	if r.pos >= len(r.data) {
		return nil, io.EOF
	}
	f, n, err := next(r.data[r.pos:], nil)
	if err != nil {
		return nil, err
	}
	r.pos += n
	return f, nil
}

// SinglePassCheck decodes 100..10000-frame streams; one examination per byte.
func SinglePassCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		fs := make([][]byte, m)
		for i := range fs {
			b := make([]byte, i%7)
			for j := range b {
				switch (i + j) % 5 {
				case 0:
					b[j] = '\\'
				case 1:
					b[j] = '\n'
				default:
					b[j] = byte('a' + j%26)
				}
			}
			fs[i] = b
		}
		s := Encode(fs)
		got, err := Decode(s)
		if err != nil || len(got) != m {
			return fmt.Errorf("single-pass m=%d: %v", m, err)
		}
		for i := range got {
			if string(got[i]) != string(fs[i]) {
				return fmt.Errorf("single-pass m=%d frame %d changed", m, i)
			}
		}
		if lastDecode.examined.Load() != int64(len(s)) || lastDecode.reread.Load() != 0 {
			return fmt.Errorf("single-pass m=%d: not one byte once", m)
		}
	}
	return nil
}
