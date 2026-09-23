// Package dec is a streaming, validating LZ77O decompressor.
package dec

import (
	"errors"
	"fmt"

	"ontology/window"
	"ontology/wire"
)

var (
	ErrTruncated      = errors.New("dec: truncated stream")
	ErrTrailing       = errors.New("dec: trailing bytes after end")
	ErrBadMagic       = errors.New("dec: bad magic or version")
	ErrZeroDistance   = errors.New("dec: match distance is zero")
	ErrDistPastOutput = errors.New("dec: distance exceeds emitted bytes")
	ErrDistOverWindow = errors.New("dec: distance exceeds window capacity")
	ErrBadTag         = errors.New("dec: unknown record tag")
	ErrLengthMismatch = errors.New("dec: end length mismatch")
	ErrChecksum       = errors.New("dec: checksum mismatch")
	ErrOutputLimit    = errors.New("dec: output size limit exceeded")
	ErrClosed         = errors.New("dec: write after terminal error")
)

// OffsetError annotates an error with the compressed-stream byte offset.
type OffsetError struct {
	Err    error
	Offset int64
}

func (e *OffsetError) Error() string { return fmt.Sprintf("%v at offset %d", e.Err, e.Offset) }
func (e *OffsetError) Unwrap() error { return e.Err }

// Config configures a Reader.
type Config struct {
	WindowCap int
	MaxOutput int64 // 0 => unlimited
}

// Reader validates and decompresses incrementally. It is not
// concurrent-safe; after any error it becomes terminal.
type Reader struct {
	cfg        Config
	win        *window.Window
	buf        []byte
	off        int64
	out        []byte
	sum        uint64
	state      state
	v1, v2     uint64
	need       int
	err        *OffsetError
}

type state int

const (
	stHeader state = iota
	stTag
	stLitLen
	stLitBytes
	stDist
	stLen
	stEndLen
	stEndSum
	stDone
)

// NewReader validates cfg and returns a decompressor.
func NewReader(cfg Config) (*Reader, error) {
	if cfg.WindowCap <= 0 {
		return nil, errors.New("dec: WindowCap must be positive")
	}
	return &Reader{cfg: cfg, win: window.New(cfg.WindowCap), sum: uint64(wire.FNVOffset64)}, nil
}

// Err returns the terminal error or nil.
func (r *Reader) Err() error {
	if r.err == nil {
		return nil
	}
	return r.err
}

// Output returns the validated decompressed prefix.
func (r *Reader) Output() []byte { return r.out }

func (r *Reader) fail(e error, off int64) bool {
	if r.err == nil {
		r.err = &OffsetError{Err: e, Offset: off}
	}
	return false
}

func (r *Reader) emitByte(b byte) {
	r.out = append(r.out, b)
	r.win.Put(b)
	r.sum ^= uint64(b)
	r.sum *= wire.FNVPrime64
}

func (r *Reader) limitOk(n uint64) bool {
	return r.cfg.MaxOutput <= 0 || uint64(r.win.Total())+n <= uint64(r.cfg.MaxOutput)
}

func (r *Reader) readVarint(next state, dst *uint64) bool {
	v, n, err := wire.ReadVarint(r.buf)
	if err != nil {
		return r.fail(err, r.off)
	}
	if n == 0 {
		return false
	}
	*dst, r.buf = v, r.buf[n:]
	r.off, r.state = r.off+int64(n), next
	return true
}

// Write feeds an arbitrary chunk of compressed bytes.
func (r *Reader) Write(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	r.buf = append(r.buf, p...)
	for r.drive() {
	}
	return len(p), r.err
}

// Close declares the stream finished; an incomplete stream is truncated.
func (r *Reader) Close() error {
	if r.err == nil && r.state != stDone {
		r.fail(ErrTruncated, r.off)
	}
	return r.err
}

func (r *Reader) drive() bool {
	switch r.state {
	case stHeader:
		if len(r.buf) < len(wire.Magic)+1 {
			return false
		}
		if string(r.buf[:len(wire.Magic)]) != wire.Magic || r.buf[len(wire.Magic)] != wire.Version {
			return r.fail(ErrBadMagic, r.off)
		}
		r.buf, r.off = r.buf[len(wire.Magic)+1:], r.off+int64(len(wire.Magic))+1
		r.state = stTag
		return true
	case stTag:
		if len(r.buf) == 0 {
			return false
		}
		t := r.buf[0]
		r.buf, r.off, r.state = r.buf[1:], r.off+1, stTag
		switch t {
		case wire.TagLiteral:
			r.state = stLitLen
		case wire.TagMatch:
			r.state = stDist
		case wire.TagFlush:
			r.state = stTag
		case wire.TagEnd:
			r.state = stEndLen
		default:
			return r.fail(ErrBadTag, r.off-1)
		}
		return true
	case stLitLen:
		return r.readVarint(stLitBytes, &r.v1)
	case stLitBytes:
		if uint64(len(r.buf)) < r.v1 {
			return false
		}
		if !r.limitOk(r.v1) {
			return r.fail(ErrOutputLimit, r.off)
		}
		for i := uint64(0); i < r.v1; i++ {
			r.emitByte(r.buf[i])
		}
		r.buf, r.off = r.buf[r.v1:], r.off+int64(r.v1)
		r.state = stTag
		return true
	case stDist:
		return r.readVarint(stLen, &r.v1)
	case stLen:
		return r.readVarint(stApplyMatch, &r.v2)
	case stApplyMatch:
		if r.v1 == 0 {
			return r.fail(ErrZeroDistance, r.matchOff)
		}
		if r.v1 > uint64(r.win.Total()) {
			return r.fail(ErrDistPastOutput, r.matchOff)
		}
		if r.v1 > uint64(r.cfg.WindowCap) {
			return r.fail(ErrDistOverWindow, r.matchOff)
		}
		if !r.limitOk(r.v2) {
			return r.fail(ErrOutputLimit, r.matchOff)
		}
		for i := uint64(0); i < r.v2; i++ {
			r.emitByte(r.win.At(int(r.v1)))
		}
		r.state = stTag
		return true
	case stEndLen:
		return r.readVarint(stEndSum, &r.v1)
	case stEndSum:
		return r.readVarint(stVerifyEnd, &r.v2)
	case stVerifyEnd:
		if r.v1 != uint64(r.win.Total()) {
			return r.fail(ErrLengthMismatch, r.off)
		}
		if r.v2 != r.sum {
			return r.fail(ErrChecksum, r.off)
		}
		if len(r.buf) != 0 {
			return r.fail(ErrTrailing, r.off)
		}
		r.state = stDone
		return false
	}
	return false
}
