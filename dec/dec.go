// Package dec is the streaming, byte-validating LZ77 decompressor.
package dec

import (
	"errors"
	"fmt"
	"hash/fnv"

	"ontology/window"
	"ontology/wire"
)

var (
	ErrBadMagic        = errors.New("dec: bad magic")
	ErrBadVersion      = errors.New("dec: unsupported version")
	ErrDistZero        = errors.New("dec: match distance is zero")
	ErrDistWindow      = errors.New("dec: match distance exceeds window capacity")
	ErrDistOutput      = errors.New("dec: match distance exceeds bytes output so far")
	ErrVarint          = errors.New("dec: varint too long or overflow")
	ErrLengthMismatch  = errors.New("dec: tail total length does not match output")
	ErrChecksum        = errors.New("dec: tail checksum mismatch")
	ErrTrailingBytes   = errors.New("dec: extra bytes after stream tail")
	ErrTruncated       = errors.New("dec: truncated stream")
	ErrOutputLimit     = errors.New("dec: output size limit exceeded")
	ErrBadConfig       = errors.New("dec: window capacity must be positive")
	ErrTerminal        = errors.New("dec: decompressor is in terminal error state")
)

// DecodeError wraps a sentinel with the byte offset in the compressed stream.
type DecodeError struct {
	Offset int64
	Err    error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("%v at compressed byte offset %d", e.Err, e.Offset)
}
func (e *DecodeError) Unwrap() error { return e.Err }

// Reader incrementally decompresses a stream; not safe for concurrent use.
type Reader struct {
	win      *window.Window
	maxOut   int64
	buf      []byte
	consumed int64
	out      []byte
	sum      fnv.Hash64
	state    int // 0 header, 1 records, 2 done, 3 failed
	terminal error
}

// NewReader builds a decompressor. maxOut<=0 means unlimited.
func NewReader(windowCap int, maxOut int64) (*Reader, error) {
	if windowCap <= 0 {
		return nil, ErrBadConfig
	}
	win, err := window.New(windowCap)
	if err != nil {
		return nil, ErrBadConfig
	}
	return &Reader{win: win, maxOut: maxOut, sum: fnv.New64a()}, nil
}

func (r *Reader) fail(off int64, err error) error {
	r.state = 3
	de := &DecodeError{Offset: off, Err: err}
	r.terminal = de
	return de
}

func (r *Reader) emit(b []byte) error {
	if r.maxOut > 0 && int64(len(r.out)+len(b)) > r.maxOut {
		return ErrOutputLimit
	}
	for i := range b {
		r.win.Add(b[i])
	}
	r.sum.Write(b)
	r.out = append(r.out, b...)
	return nil
}

func (r *Reader) copyMatch(d, l uint64) error {
	if r.maxOut > 0 && int64(len(r.out))+int64(l) > r.maxOut {
		return ErrOutputLimit
	}
	for i := uint64(0); i < l; i++ {
		b, err := r.win.Get(int64(d))
		if err != nil {
			return err
		}
		r.win.Add(b)
		r.sum.Write([]byte{b})
		r.out = append(r.out, b)
	}
	return nil
}

// Write feeds an arbitrary chunk of the compressed stream.
func (r *Reader) Write(p []byte) (int, error) {
	if r.state == 3 {
		return 0, r.terminal
	}
	if r.state == 2 {
		if len(p) > 0 {
			return 0, r.fail(r.consumed, ErrTrailingBytes)
		}
		return 0, nil
	}
	r.buf = append(r.buf, p...)
	n := len(p)
	if err := r.parse(); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *Reader) parse() error {
	for {
		if r.state == 0 {
			hdr := wire.AppendHeader(nil)
			if len(r.buf) < len(hdr) {
				return nil
			}
			if string(r.buf[:len(wire.Magic)]) != wire.Magic {
				return r.fail(r.consumed, ErrBadMagic)
			}
			v, k, err := wire.DecodeVarint(r.buf[len(wire.Magic):])
			if err != nil {
				return r.fail(r.consumed+int64(len(wire.Magic)), ErrVarint)
			}
			if k == 0 {
				return nil
			}
			if v != wire.Version {
				return r.fail(r.consumed+int64(len(wire.Magic)), ErrBadVersion)
			}
			r.advance(len(hdr))
			r.state = 1
		}
		tagOff := r.consumed
		t, k, err := wire.DecodeVarint(r.buf)
		if err != nil {
			return r.fail(tagOff, ErrVarint)
		}
		if k == 0 {
			return nil
		}
		if wire.IsMatch(t) {
			d := wire.MatchDist(t)
			l, lk, lerr := wire.DecodeVarint(r.buf[k:])
			if lerr != nil {
				return r.fail(tagOff+int64(k), ErrVarint)
			}
			if lk == 0 {
				return nil
			}
			r.advance(k + lk)
			if err := r.checkMatch(tagOff, d, l); err != nil {
				return err
			}
			if err := r.copyMatch(d, l); err != nil {
				return r.fail(tagOff, err)
			}
			continue
		}
		switch t {
		case wire.TagLiteral:
			if !r.parseLiteral(tagOff, k) {
				return nil
			}
		case wire.TagFlush:
			r.advance(k)
		case wire.TagEnd:
			if !r.parseEnd(tagOff, k) {
				return nil
			}
			return nil
		default:
			return r.fail(tagOff, ErrTruncated)
		}
	}
}

func (r *Reader) advance(n int) {
	r.buf = r.buf[n:]
	r.consumed += int64(n)
}

func (r *Reader) checkMatch(off, d, l int64off) error

func (r *Reader) checkMatch(off int64, d, l uint64) error {
	if d == 0 {
		return r.fail(off, ErrDistZero)
	}
	if d > uint64(r.win.Cap()) {
		return r.fail(off, ErrDistWindow)
	}
	if d > uint64(r.win.Len()) {
		return r.fail(off, ErrDistOutput)
	}
	if l == 0 {
		return r.fail(off, ErrTruncated)
	}
	return nil
}

func (r *Reader) parseLiteral(off int64, k int) bool {
	n, nk, err := wire.DecodeVarint(r.buf[k:])
	if err != nil {
		r.fail(off+int64(k), ErrVarint)
		return false
	}
	if nk == 0 {
		return false
}
	start := k + nk
	if int64(len(r.buf)-start) < int64(n) {
		return false
	}
	if err := r.emit(r.buf[start : start+int(n)]); err != nil {
		r.fail(off, err)
		return false
	}
	r.advance(start + int(n))
	return true
}

func (r *Reader) parseEnd(off int64, k int) bool {
	total, t1, e1 := wire.DecodeVarint(r.buf[k:])
	if e1 != nil {
		r.fail(off+int64(k), ErrVarint)
		return false
	}
	if t1 == 0 {
		return false
	}
	csum, t2, e2 := wire.DecodeVarint(r.buf[k+t1:])
	if e2 != nil {
		r.fail(off+int64(k+t1), ErrVarint)
		return false
	}
	if t2 == 0 {
		return false
	}
	r.advance(k + t1 + t2)
	if total != uint64(len(r.out)) {
		r.fail(off, ErrLengthMismatch)
		return false
	}
	if csum != r.sum.Sum64() {
		r.fail(off, ErrChecksum)
		return false
	}
	if len(r.buf) > 0 {
		r.fail(r.consumed, ErrTrailingBytes)
		return false
	}
	r.state = 2
	return true
}

// Close must be called after the entire stream has been written. It reports
// ErrTruncated when the stream ends early.
func (r *Reader) Close() error {
	if r.state == 3 {
		return r.terminal
	}
	if r.state != 2 {
		return r.fail(r.consumed, ErrTruncated)
	}
	return nil
}

// Output returns a copy of all bytes decompressed so far.
func (r *Reader) Output() []byte {
	out := make([]byte, len(r.out))
	copy(out, r.out)
	return out
}
