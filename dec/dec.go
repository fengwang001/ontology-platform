// Package dec is a streaming LZ77 decompressor.
package dec

import (
	"errors"
	"hash/crc32"

	"ontology/window"
	"ontology/wire"
)

var (
	ErrBadMagic       = errors.New("dec: bad stream magic")
	ErrBadVersion     = errors.New("dec: unsupported version")
	ErrDistanceZero   = errors.New("dec: match distance is zero")
	ErrDistanceOutput = errors.New("dec: distance exceeds bytes produced")
	ErrDistanceWindow = errors.New("dec: distance exceeds window capacity")
	ErrVarint         = errors.New("dec: malformed varint")
	ErrLengthMismatch = errors.New("dec: stream length mismatch")
	ErrChecksum       = errors.New("dec: checksum mismatch")
	ErrTrailingBytes  = errors.New("dec: trailing bytes after end")
	ErrTruncated      = errors.New("dec: truncated stream")
	ErrUnknownTag     = errors.New("dec: unknown record tag")
	ErrInvalidConfig  = errors.New("dec: invalid configuration")
	ErrOutputLimit    = errors.New("dec: output size limit exceeded")
	ErrClosedWrite    = errors.New("dec: write after terminal state")
)

// Error pairs a sentinel cause with the byte offset in the compressed stream.
type Error struct {
	Cause  error
	Offset int
}

func (e *Error) Error() string { return e.Cause.Error() }
func (e *Error) Unwrap() error { return e.Cause }

// Reader decompresses bytes fed through Write. A single instance is not
// safe for concurrent use.
type Reader struct {
	win      *window.Window
	maxOut   int
	out      []byte
	buf      []byte // unconsumed compressed bytes
	consumed int    // bytes of the whole stream already consumed
	state    int
	hdr      int
	tag      byte
	a, b     uint64
	an, bn   int
	litLen   int
	checksum uint32
	ended    bool
	terminal *Error
}

func NewReader(windowCap, maxOutput int) (*Reader, error) {
	if windowCap <= 0 || maxOutput < 0 {
		return nil, ErrInvalidConfig
	}
	return &Reader{win: window.New(windowCap), maxOut: maxOutput}, nil
}

// Output returns a copy of all bytes produced so far.
func (r *Reader) Output() []byte { out := make([]byte, len(r.out)); copy(out, r.out); return out }

func (r *Reader) fail(err error) *Error {
	if r.terminal == nil {
		r.terminal = &Error{Cause: err, Offset: r.consumed}
	}
	return r.terminal
}

func (r *Reader) allow(n int) error {
	if r.maxOut > 0 && len(r.out)+n > r.maxOut {
		return r.fail(ErrOutputLimit)
	}
	return nil
}

func (r *Reader) readVarint() (uint64, bool) {
	v, n, more, err := wire.ConsumeUvarint(r.buf)
	if err != nil {
		r.consumed += len(r.buf)
		r.buf = r.buf[:0]
		r.fail(ErrVarint)
		return 0, false
	}
	if more {
		return 0, false
	}
	r.consumed += n
	r.buf = r.buf[n:]
	return v, true
}

func (r *Reader) parse() {
	wantMagic := []byte{wire.Magic0, wire.Magic1, wire.Magic2, wire.Magic3}
parse:
	for r.terminal == nil && !r.ended {
		if r.hdr < 4 {
			if len(r.buf) == 0 {
				return
			}
			if r.buf[0] != wantMagic[r.hdr] {
				r.consumed++
				r.buf = r.buf[1:]
				r.fail(ErrBadMagic)
				return
			}
			r.buf = r.buf[1:]
			r.consumed++
			r.hdr++
			continue
		}
		if r.state == 0 {
			v, ok := r.readVarint()
			if r.terminal != nil || !ok {
				return
			}
			if v != wire.Version {
				r.fail(ErrBadVersion)
				return
			}
			r.state = 1
		}
		for r.state == 1 {
			if len(r.buf) == 0 {
				return
			}
			r.tag = r.buf[0]
			r.buf = r.buf[1:]
			r.consumed++
			switch r.tag {
			case wire.TagFlush:
				continue
			case wire.TagLiteral:
				r.state = 2
			case wire.TagMatch:
				r.state = 4
			case wire.TagEnd:
				r.state = 7
			default:
				r.fail(ErrUnknownTag)
				return
			}
		}
		for r.state != 1 && r.state != 9 && r.terminal == nil {
			switch r.state {
			case 2:
				v, ok := r.readVarint()
				if r.terminal != nil || !ok {
					return
				}
				r.a, r.litLen, r.state = v, 0, 3
			case 3:
				need := int(r.a) - r.litLen
				if len(r.buf) < need {
					need = len(r.buf)
				}
				if err := r.allow(need); err != nil {
					return
				}
				r.out = append(r.out, r.buf[:need]...)
				r.win.Push(r.buf[:need])
				r.checksum = crc32.Update(r.checksum, crc32.IEEETable, r.buf[:need])
				r.litLen += need
				r.consumed += need
				r.buf = r.buf[need:]
				if r.litLen == int(r.a) {
					r.state = 1
				}
			case 4:
				v, ok := r.readVarint()
				if r.terminal != nil {
					return
				}
				if !ok {
					return
				}
				r.a, r.state = v, 5
			case 5:
				v, ok := r.readVarint()
				if r.terminal != nil {
					return
				}
				if !ok {
					return
				}
				r.b, r.state = v, 6
			case 6:
				dist, length := int(r.a), int(r.b)
				switch {
				case dist == 0:
					r.fail(ErrDistanceZero)
				case dist > r.win.Cap():
					r.fail(ErrDistanceWindow)
				case dist > len(r.out):
					r.fail(ErrDistanceOutput)
				}
				if r.terminal != nil {
					return
				}
				if err := r.allow(length); err != nil {
					return
				}
				start := len(r.out)
				for k := 0; k < length; k++ {
					r.out = append(r.out, r.out[start-dist+k])
				}
				r.win.Push(r.out[start:])
				r.checksum = crc32.Update(r.checksum, crc32.IEEETable, r.out[start:])
				r.state = 1
			case 7:
				v, ok := r.readVarint()
				if r.terminal != nil || !ok {
					return
				}
				r.a, r.state = v, 8
			case 8:
				v, ok := r.readVarint()
				if r.terminal != nil || !ok {
					return
				}
				r.b, r.state = v, 9
				if uint64(len(r.out)) != r.a {
					r.fail(ErrLengthMismatch)
					return
				}
				if uint64(r.checksum) != r.b {
					r.fail(ErrChecksum)
					return
				}
			}
		}
		if r.state != 9 {
			if len(r.buf) == 0 {
				return // waiting for more bytes
			}
			continue parse
		}
		if len(r.buf) > 0 {
			r.consumed += len(r.buf)
			r.buf = r.buf[:0]
			r.fail(ErrTrailingBytes)
			return
		}
		r.ended = true
		return
	}
}

// Write feeds compressed bytes. After a terminal error the same error is
// returned on every later Write; produced output is retained.
func (r *Reader) Write(p []byte) (int, error) {
	if r.terminal != nil {
		return 0, r.terminal
	}
	r.buf = append(r.buf, p...)
	r.parse()
	if r.terminal != nil {
		return 0, r.terminal
	}
	return len(p), nil
}

// Close finalizes the stream. Any incomplete stream is reported as truncated.
func (r *Reader) Close() error {
	if r.terminal != nil {
		return r.terminal
	}
	if !r.ended {
		return r.fail(ErrTruncated)
	}
	return nil
}
