// Package dec implements the streaming LZ77 decompressor.
package dec

import (
	"errors"
	"fmt"
	"hash/crc32"

	"ontology/window"
	"ontology/wire"
)

// Sentinel and named errors; all are decidable.
var (
	ErrBadHeader      = errors.New("dec: bad magic or version")
	ErrZeroDistance   = errors.New("dec: zero back-reference distance")
	ErrDistanceOutput = errors.New("dec: distance exceeds bytes produced")
	ErrDistanceWindow = errors.New("dec: distance exceeds window capacity")
	ErrSizeMismatch   = errors.New("dec: trailer size mismatch")
	ErrChecksum       = errors.New("dec: checksum mismatch")
	ErrTrailingBytes  = errors.New("dec: trailing bytes after trailer")
	ErrTruncated      = errors.New("dec: truncated stream")
	ErrOutputLimit    = errors.New("dec: output limit exceeded")
	ErrBadLength      = errors.New("dec: match length below minimum")
)

// OffsetError wraps an error with the byte offset in the compressed stream.
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("%v at byte %d", e.Err, e.Offset)
}

func (e *OffsetError) Unwrap() error { return e.Err }

// Config configures a decompressor.
type Config struct {
	WindowCap int
	MaxOutput uint64
}

type state int

const (
	stHeader state = iota
	stTag
	stLitLen
	stLitCopy
	stMatchDist
	stMatchLen
	stEndSize
	stEndCRC
	stDone
)

// Decoder consumes a compressed stream in arbitrary Write chunk sizes. It is
// not safe for concurrent use.
type Decoder struct {
	win     *window.Window
	out     []byte
	state   state
	off     int
	total   uint64
	crc     uint32
	wantLen uint64
	wantCRC uint32
	litN    int
	dist    int
	fatal   error
	maxOut  uint64
}

// New builds a decoder; zero WindowCap uses 64KiB.
func New(c Config) (*Decoder, error) {
	wc := c.WindowCap
	if wc == 0 {
		wc = 1 << 16
	}
	w, err := window.New(wc)
	if err != nil {
		return nil, err
	}
	return &Decoder{win: w, state: stHeader, crc: crc32.ChecksumIEEE(nil), maxOut: c.MaxOutput}, nil
}

func (e *Decoder) fail(err error) error {
	if e.fatal == nil {
		e.fatal = &OffsetError{Offset: e.off, Err: err}
	}
	return e.fatal
}

// Output returns all decompressed bytes accumulated so far.
func (e *Decoder) Output() []byte { return e.out }

func (e *Decoder) emit(c byte) {
	e.out = append(e.out, c)
	e.win.Add(c)
	e.total++
}

// Write feeds compressed bytes. The same byte is never interpreted twice.
func (e *Decoder) Write(p []byte) (int, error) {
	n := len(p)
	if e.fatal != nil {
		return 0, e.fatal
	}
	for len(p) > 0 {
		if err := e.step(&p); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (e *Decoder) readVar(p *[]byte, target *uint64) bool {
	v, k, ok, err := wire.ReadUvarint(*p)
	if err != nil {
		_ = e.fail(err)
		return false
	}
	if !ok {
		return false
	}
	*target = v
	*p = (*p)[k:]
	e.off += k
	return true
}

func (e *Decoder) step(p *[]byte) error {
	c := (*p)[0]
	switch e.state {
	case stHeader:
		h := e.off
		if h < len(wire.Header) {
			if c != wire.Header[h] {
				return e.fail(ErrBadHeader)
			}
			e.off++
			*p = (*p)[1:]
			if e.off == len(wire.Header) {
				e.state = stTag
			}
		}
	case stTag:
		e.off++
		*p = (*p)[1:]
		switch c {
		case wire.TagLiteral:
			e.wantLen = 0
			e.state = stLitLen
		case wire.TagMatch:
			e.wantLen = 0
			e.state = stMatchDist
		case wire.TagFlush:
		case wire.TagEnd:
			e.state = stEndSize
		default:
			return e.fail(ErrBadHeader)
		}
	case stLitLen:
		var v uint64
		if !e.readVar(p, &v) {
			return nil
		}
		e.litN = int(v)
		e.state = stLitCopy
	case stLitCopy:
		if e.maxOut > 0 && (e.total > e.maxOut || uint64(e.litN) > e.maxOut-e.total) {
			return e.fail(ErrOutputLimit)
		}
		n := min(e.litN, len(*p))
		if n > 0 {
			e.out = append(e.out, (*p)[:n]...)
			for _, b := range (*p)[:n] {
				e.win.Add(b)
			}
			e.total += uint64(n)
			e.crc = crc32.Update(e.crc, crc32.IEEETable, (*p)[:n])
			e.litN -= n
			e.off += n
			*p = (*p)[n:]
		}
		if e.litN == 0 {
			e.state = stTag
		} else if n == 0 {
			return nil
		}
	case stMatchDist:
		var v uint64
		if !e.readVar(p, &v) {
			return nil
		}
		e.dist = int(v)
		if e.dist == 0 {
			return e.fail(ErrZeroDistance)
		}
		if uint64(e.dist) > e.total {
			return e.fail(ErrDistanceOutput)
		}
		if e.dist > e.win.Cap() {
			return e.fail(ErrDistanceWindow)
		}
		e.state = stMatchLen
	case stMatchLen:
		var v uint64
		if !e.readVar(p, &v) {
			return nil
		}
		if v < 3 {
			return e.fail(ErrBadLength)
		}
		if e.maxOut > 0 && (e.total > e.maxOut || v > e.maxOut-e.total) {
			return e.fail(ErrOutputLimit)
		}
		start := len(e.out)
		for i := uint64(0); i < v; i++ {
			b := e.win.Byte(e.dist) // byte-by-byte: valid for overlap
			e.out = append(e.out, b)
			e.win.Add(b)
		}
		e.total += v
		e.crc = crc32.Update(e.crc, crc32.IEEETable, e.out[start:])
		e.state = stTag
	case stEndSize:
		var v uint64
		if !e.readVar(p, &v) {
			return nil
		}
		e.wantLen = v
		e.state = stEndCRC
	case stEndCRC:
		var v uint64
		if !e.readVar(p, &v) {
			return nil
		}
		e.wantCRC = uint32(v)
		if e.total != e.wantLen {
			return e.fail(ErrSizeMismatch)
		}
		if e.crc != e.wantCRC {
			return e.fail(ErrChecksum)
		}
		e.state = stDone
	case stDone:
		return e.fail(ErrTrailingBytes)
	}
	return e.fatal
}

// Close reports truncation for any unfinished stream.
func (e *Decoder) Close() error {
	if e.fatal != nil {
		return e.fatal
	}
	if e.state != stDone {
		return e.fail(ErrTruncated)
	}
	return nil
}
