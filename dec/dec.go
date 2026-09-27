// Package dec is the streaming validator/decompressor. It validates every byte
// and reports corruption with the absolute byte offset in the compressed
// stream. A single Decoder is not safe for concurrent use.
package dec

import (
	"errors"
	"fmt"

	"ontology/window"
	"ontology/wire"
)

// Sentinel corruption kinds; all are wrapped in DecodeError with an offset.
var (
	ErrBadHeader    = errors.New("dec: bad magic or version")
	ErrZeroDistance = errors.New("dec: back-reference distance is zero")
	ErrDistEmitted  = errors.New("dec: distance exceeds emitted bytes")
	ErrDistWindow   = errors.New("dec: distance exceeds window capacity")
	ErrVarint       = errors.New("dec: varint too long or overflow")
	ErrLength       = errors.New("dec: trailer length mismatch")
	ErrChecksum     = errors.New("dec: trailer checksum mismatch")
	ErrTrailing     = errors.New("dec: trailing bytes after end")
	ErrTruncated    = errors.New("dec: truncated stream")
	ErrOutputLimit  = errors.New("dec: output limit exceeded")
)

// DecodeError pairs a sentinel cause with a compressed-stream byte offset.
type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%v at byte %d", e.Err, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

// Decoder consumes arbitrarily fragmented compressed input.
type Decoder struct {
	win    *window.Window
	maxOut int
	out    []byte
	buf    []byte // unconsumed compressed bytes
	pos    int    // absolute compressed bytes consumed
	total  int
	hash   uint64
	state  state
	litLen int
	mtDist int
	mtLen  int
	ended  bool
	fatal  *DecodeError
}

type state int

const (
	stTag state = iota
	stLitData
	stMatchDist
	stMatchLen
	stEndLen
	stEndSum
)

// New creates a decoder. windowCap must be > 0; maxOut <= 0 means no limit.
func New(windowCap, maxOut int) (*Decoder, error) {
	win, err := window.New(windowCap)
	if err != nil {
		return nil, err
	}
	return &Decoder{win: win, maxOut: maxOut, hash: 14695981039346656037}, nil
}

// Output returns a copy of the bytes emitted so far (kept after a fatal error).
func (d *Decoder) Output() []byte {
	p := make([]byte, len(d.out))
	copy(p, d.out)
	return p
}

func (d *Decoder) fail(off int, err error) error {
	if d.fatal == nil {
		d.fatal = &DecodeError{Offset: off, Err: err}
	}
	return d.fatal
}

func (d *Decoder) emit(b byte) error {
	if d.maxOut > 0 && len(d.out) >= d.maxOut {
		return d.fail(d.pos, ErrOutputLimit)
	}
	d.out = append(d.out, b)
	d.win.Push(b)
	d.hash ^= uint64(b)
	d.hash *= 1099511628211
	return nil
}

// run processes everything currently buffered, stopping only on a fatal error
// or when more input bytes are required.
func (d *Decoder) run() error {
	for {
		switch d.state {
		case stTag:
			if d.pos < 3 { // fixed 3-byte header, may arrive in fragments
				want := wire.Header(nil)
				for d.pos < 3 {
					if len(d.buf) == 0 {
						return nil
					}
					if d.buf[0] != want[d.pos] {
						return d.fail(d.pos, ErrBadHeader)
					}
					d.buf = d.buf[1:]
					d.pos++
				}
			}
			if len(d.buf) == 0 {
				return nil
			}
			v, rest, ok, err := wire.ReadUvarint(d.buf)
			if err != nil {
				return d.fail(d.pos, ErrVarint)
			}
			if !ok {
				return nil
			}
			d.pos += len(d.buf) - len(rest)
			d.buf = rest
			switch v & 3 {
			case 0:
				d.litLen = int(v >> 2)
				d.state = stLitData
			case 1: // flush marker: nothing to do
			case 2:
				d.state = stMatchDist
			case 3:
				d.state = stEndLen
			}
		case stMatchDist, stMatchLen, stEndLen, stEndSum:
			v, rest, ok, err := wire.ReadUvarint(d.buf)
			if err != nil {
				return d.fail(d.pos, ErrVarint)
			}
			if !ok {
				return nil
			}
			d.pos += len(d.buf) - len(rest)
			d.buf = rest
			switch d.state {
			case stMatchDist:
				d.mtDist = int(v)
				if d.mtDist == 0 {
					return d.fail(d.pos, ErrZeroDistance)
				}
				if d.mtDist > len(d.out) {
					return d.fail(d.pos, ErrDistEmitted)
				}
				if d.mtDist > d.win.Cap() {
					return d.fail(d.pos, ErrDistWindow)
				}
				d.state = stMatchLen
			case stMatchLen:
				d.mtLen = int(v)
				if d.maxOut > 0 && d.mtLen > d.maxOut-len(d.out) {
					return d.fail(d.pos, ErrOutputLimit)
				}
				for k := 0; k < d.mtLen; k++ {
					if err := d.emit(d.out[len(d.out)-d.mtDist]); err != nil {
						return err
					}
				}
				d.state = stTag
			case stEndLen:
				d.total = int(v)
				d.state = stEndSum
			case stEndSum:
				d.ended = true
				if d.total != len(d.out) {
					return d.fail(d.pos, ErrLength)
				}
				if v != d.hash {
					return d.fail(d.pos, ErrChecksum)
				}
				d.state = stTag
			}
		case stLitData:
			if len(d.buf) < d.litLen {
				return nil
			}
			for _, b := range d.buf[:d.litLen] {
				if err := d.emit(b); err != nil {
					return err
				}
			}
			d.buf = d.buf[d.litLen:]
			d.pos += d.litLen
			d.litLen = 0
			d.state = stTag
		}
		if d.ended && (d.state == stTag) {
			if len(d.buf) > 0 {
				return d.fail(d.pos, ErrTrailing)
			}
		}
	}
}

// Write feeds a fragment of the compressed stream; bytes are never interpreted
// twice. After a fatal error the same error is returned forever.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.fatal != nil {
		return 0, d.fatal
	}
	if d.ended {
		if len(p) > 0 {
			return 0, d.fail(d.pos, ErrTrailing)
		}
		return 0, nil
	}
	n := len(p)
	d.buf = append(d.buf, p...)
	if err := d.run(); err != nil {
		return n, err
	}
	return n, nil
}

// Close reports truncation when the trailer has not been fully consumed.
func (d *Decoder) Close() error {
	if d.fatal != nil {
		return d.fatal
	}
	if !d.ended {
		return d.fail(d.pos, ErrTruncated)
	}
	return nil
}
