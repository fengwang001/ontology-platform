// Package frame implements flag-byte framing: Encode wraps a payload into a
// frame, Parser decodes a byte stream with a three-state machine.
package frame

import (
	"errors"

	"ontology/esc"
)

var (
	ErrBadEscape       = errors.New("frame: ESC followed by FLAG")
	ErrTruncatedEscape = errors.New("frame: flush while awaiting escaped byte")
	ErrTruncatedFrame  = errors.New("frame: flush with unterminated frame")
)

type state uint8

const (
	stOut state = iota // outside a frame; only Flag matters
	stIn               // inside a frame; collecting payload
	stEsc              // last byte was Esc; next byte is XOR-mapped
)

// Parser is a streaming frame decoder. State persists across Feed calls.
type Parser struct {
	st      state
	buf     []byte   // payload prefix of the frame in progress
	frames  [][]byte // completed frames
	checked int      // total bytes inspected since creation (unexported)
}

// New returns a Parser in the OUT state.
func New() *Parser { return &Parser{st: stOut} }

// Encode returns FLAG + escaped(payload) + FLAG.
func Encode(payload []byte) []byte {
	out := make([]byte, 0, len(payload)+2)
	out = append(out, esc.Flag)
	for _, b := range payload {
		if esc.NeedsEscape(b) {
			out = append(out, esc.Esc, esc.Map(b))
		} else {
			out = append(out, b)
		}
	}
	return append(out, esc.Flag)
}

// Feed consumes chunk and returns the frames it completes. On error nothing
// is committed: state, frames and the byte counter stay as before the call.
func (p *Parser) Feed(chunk []byte) ([][]byte, error) {
	st, buf, frames, checked := p.st, p.buf, p.frames, p.checked
	var done [][]byte
	for _, c := range chunk {
		checked++
		switch st {
		case stOut:
			if c == esc.Flag {
				st = stIn
				buf = nil // fresh backing array: never alias committed state
			}
		case stIn:
			switch c {
			case esc.Flag:
				f := append([]byte(nil), buf...)
				frames, done = append(frames, f), append(done, f)
				st = stOut
			case esc.Esc:
				st = stEsc
			default:
				buf = append(buf, c)
			}
		case stEsc:
			if c == esc.Flag {
				return nil, ErrBadEscape
			}
			buf = append(buf, esc.Map(c))
			st = stIn
		}
	}
	p.st, p.buf, p.frames, p.checked = st, buf, frames, checked
	return done, nil
}

// Flush reports a truncated escape or frame; it never mutates state.
func (p *Parser) Flush() error {
	switch p.st {
	case stEsc:
		return ErrTruncatedEscape
	case stIn:
		return ErrTruncatedFrame
	}
	return nil
}

// Frames returns a deep-copy snapshot of the completed frames.
func (p *Parser) Frames() [][]byte {
	out := make([][]byte, len(p.frames))
	for i, f := range p.frames {
		out[i] = append([]byte(nil), f...)
	}
	return out
}

// SinglePass reports whether feeding a valid m-byte stream one byte at a
// time inspects exactly m bytes in total. It exposes only a verdict; the
// counter itself is never readable through any exported API.
func SinglePass(m int) bool {
	if m < 2 {
		return false
	}
	stream := Encode(make([]byte, m-2)) // zero bytes need no escaping
	p := New()
	for i := 0; i < len(stream); i++ {
		if _, err := p.Feed(stream[i : i+1]); err != nil {
			return false
		}
	}
	return p.Flush() == nil && p.checked == m
}
