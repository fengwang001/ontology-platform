// Package dec is a streaming decompressor that validates the wire stream.
//
// A single Decoder is not safe for concurrent use; use separate decoders.
package dec

import (
	"errors"
	"fmt"
	"hash"
	"hash/fnv"

	"ontology/wire"
)

var (
	ErrTruncated      = errors.New("dec: truncated stream")
	ErrBadMagic       = errors.New("dec: bad magic")
	ErrBadVersion     = errors.New("dec: unsupported version")
	ErrZeroDistance   = errors.New("dec: zero back-reference distance")
	ErrDistanceWindow = errors.New("dec: distance exceeds window capacity")
	ErrDistanceOutput = errors.New("dec: distance exceeds output size")
	ErrLengthMismatch = errors.New("dec: end total length mismatch")
	ErrChecksum       = errors.New("dec: end checksum mismatch")
	ErrTrailingBytes  = errors.New("dec: trailing bytes after end")
	ErrOutputLimit    = errors.New("dec: output size limit exceeded")
	ErrBadTag         = errors.New("dec: bad record tag")
)

// CorruptError wraps a sentinel with the byte offset in the compressed stream.
type CorruptError struct {
	Offset int
	Err    error
}

func (c *CorruptError) Error() string {
	return fmt.Sprintf("%v at byte offset %d", c.Err, c.Offset)
}

func (c *CorruptError) Unwrap() error { return c.Err }

type Decoder struct {
	maxOut int64
	buf    []byte
	off    int
	out    []byte
	winCap int
	sum    hash.Hash64
	done   bool
	fatal  error
	// pending literal copy state
	litN int
}

func New(maxOutput int64) *Decoder {
	return &Decoder{maxOut: maxOutput, sum: fnv.New64a()}
}

func (d *Decoder) fail(err error) error {
	if d.fatal == nil {
		d.fatal = &CorruptError{Offset: d.off, Err: err}
	}
	return d.fatal
}

func (d *Decoder) readVar() (uint64, bool) {
	v, n, err := wire.ReadUvarint(d.buf[d.off:])
	if err != nil {
		d.off += n
		d.fail(err)
		return 0, false
	}
	if n == 0 {
		return 0, false
	}
	d.off += n
	return v, true
}

func (d *Decoder) emit(p []byte) error {
	if int64(len(d.out)+len(p)) > d.maxOut {
		return d.fail(ErrOutputLimit)
	}
	d.out = append(d.out, p...)
	d.sum.Write(p)
	return nil
}

// Write feeds an arbitrary chunk of the compressed stream.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.fatal != nil {
		return 0, d.fatal
	}
	d.buf = append(d.buf, p...)
	n := len(p)
	if err := d.process(); err != nil {
		return n, err
	}
	return n, nil
}

func (d *Decoder) header() error {
	winCap, _, used, err := wire.ReadHeader(d.buf)
	if err != nil {
		switch {
		case errors.Is(err, wire.ErrBadMagic):
			d.off = 0
			return d.fail(ErrBadMagic)
		case errors.Is(err, wire.ErrBadVersion):
			d.off = used
			return d.fail(ErrBadVersion)
		case errors.Is(err, wire.ErrVarintLong):
			d.off = used
			return d.fail(wire.ErrVarintLong)
		default:
			return d.fail(err)
		}
	}
	if used == 0 {
		return nil
	}
	d.winCap = winCap
	d.off = used
	return nil
}

func (d *Decoder) process() error {
	if d.winCap == 0 {
		if err := d.header(); err != nil || d.winCap == 0 {
			return err
		}
	}
	for {
		if d.litN > 0 {
			take := d.litN
			if take > len(d.buf)-d.off {
				take = len(d.buf) - d.off
			}
			if take > 0 {
				if err := d.emit(d.buf[d.off : d.off+take]); err != nil {
					return err
				}
				d.off += take
				d.litN -= take
			}
			if d.litN > 0 {
				return nil
			}
		}
		if d.off >= len(d.buf) {
			return nil
		}
		tag, ok := d.readVar()
		if !ok {
			return d.fatal
		}
		switch tag {
		case wire.TagLit:
			n, ok := d.readVar()
			if !ok {
				return d.fatal
			}
			d.litN = int(n)
		case wire.TagMatch:
			dv, ok := d.readVar()
			if !ok {
				return d.fatal
			}
			lv, ok := d.readVar()
			if !ok {
				return d.fatal
			}
			dist, length := int(dv)+1, int(lv)+1
			if err := d.copyMatch(dist, length); err != nil {
				return err
			}
		case wire.TagFlush:
		case wire.TagEnd:
			total, ok := d.readVar()
			if !ok {
				return d.fatal
			}
			csum, ok := d.readVar()
			if !ok {
				return d.fatal
			}
			if int(total) != len(d.out) {
				return d.fail(ErrLengthMismatch)
			}
			if csum != d.sum.Sum64() {
				return d.fail(ErrChecksum)
			}
			d.done = true
			if d.off < len(d.buf) {
				return d.fail(ErrTrailingBytes)
			}
			d.buf = d.buf[:0]
			d.off = 0
			return nil
		default:
			return d.fail(ErrBadTag)
		}
	}
}

func (d *Decoder) copyMatch(dist, length int) error {
	if dist == 0 {
		return d.fail(ErrZeroDistance)
	}
	if dist > d.winCap {
		return d.fail(ErrDistanceWindow)
	}
	if dist > len(d.out) {
		return d.fail(ErrDistanceOutput)
	}
	if int64(len(d.out)+length) > d.maxOut {
		return d.fail(ErrOutputLimit)
	}
	for i := 0; i < length; i++ {
		d.out = append(d.out, d.out[len(d.out)-dist])
	}
	d.sum.Write(d.out[len(d.out)-length:])
	return nil
}

func (d *Decoder) Close() error {
	if d.fatal != nil {
		return d.fatal
	}
	if !d.done || d.off < len(d.buf) {
		return d.fail(ErrTruncated)
	}
	return nil
}

func (d *Decoder) Output() []byte { return d.out }

func Decompress(data []byte, maxOutput int64) ([]byte, error) {
	d := New(maxOutput)
	if _, err := d.Write(data); err != nil {
		return d.Output(), err
	}
	if err := d.Close(); err != nil {
		return d.Output(), err
	}
	return d.Output(), nil
}
