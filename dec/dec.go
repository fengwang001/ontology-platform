// Package dec implements a streaming LZ77 decompressor. A single
// Decoder is not safe for concurrent use.
package dec

import (
	"errors"
	"fmt"
	"hash/crc32"

	"ontology/window"
	"ontology/wire"
)

// Distinguishable stream corruption errors (wrapped with the offset).
var (
	ErrBadMagic    = errors.New("dec: bad magic")
	ErrBadVersion  = errors.New("dec: bad version")
	ErrBadTag      = errors.New("dec: unknown record tag")
	ErrVarint      = errors.New("dec: varint overflow")
	ErrDistZero    = errors.New("dec: back-reference distance is zero")
	ErrDistWindow  = errors.New("dec: distance exceeds window capacity")
	ErrDistHistory = errors.New("dec: distance exceeds bytes produced")
	ErrLength      = errors.New("dec: trailer length mismatch")
	ErrChecksum    = errors.New("dec: trailer checksum mismatch")
	ErrTrailing    = errors.New("dec: bytes after stream end")
	ErrTruncated   = errors.New("dec: truncated stream")
	ErrOutputLimit = errors.New("dec: output limit exceeded")
)

// Config tunes a Decoder. MaxOutput <= 0 means unlimited.
type Config struct {
	Window    int
	MaxOutput int64
}

// Decoder incrementally decodes a stream fed through Write.
type Decoder struct {
	win      *window.Window
	max, off int64
	buf, out []byte
	crc      uint32
	stage    int // 0 header, 1 records, 2 done
	err      error
}

// New creates a Decoder (nil cfg = 32 KiB window, unlimited output).
func New(cfg *Config) (*Decoder, error) {
	c := Config{Window: 1 << 15}
	if cfg != nil {
		c = *cfg
	}
	if c.MaxOutput <= 0 {
		c.MaxOutput = 1 << 62
	}
	win, err := window.New(c.Window) // rejects Window <= 0
	if err != nil {
		return nil, err
	}
	return &Decoder{win: win, max: c.MaxOutput}, nil
}

// Output returns the bytes decoded so far.
func (d *Decoder) Output() []byte { return d.out }

func (d *Decoder) fail(kind error, off int64) {
	d.err = fmt.Errorf("offset %d: %w", off, kind)
}

// Write consumes more of the stream. After an error the Decoder is
// terminal and every later Write returns that same error.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	d.buf = append(d.buf, p...)
	d.parse()
	if d.err != nil {
		return 0, d.err
	}
	return len(p), nil
}

// Close reports ErrTruncated unless the stream ended cleanly.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.stage != 2 {
		return ErrTruncated
	}
	return nil
}

func (d *Decoder) parse() {
	for d.err == nil && len(d.buf) > 0 {
		switch d.stage {
		case 0:
			if len(d.buf) < wire.HeaderLn {
				return
			}
			for i, b := range wire.Header() {
				if d.buf[i] == b {
					continue
				}
				kind := ErrBadMagic
				if i == 3 {
					kind = ErrBadVersion
				}
				d.fail(kind, d.off+int64(i))
				break
			}
			d.consume(wire.HeaderLn)
			d.stage = 1
		case 1:
			if !d.record() {
				return
			}
		case 2:
			d.fail(ErrTrailing, d.off)
		}
	}
}

// record decodes one record; false means more input is needed.
func (d *Decoder) record() bool {
	if d.buf[0] == wire.TagFlush {
		d.consume(1)
		return true
	}
	v1, k1 := d.uvarint(1)
	if k1 == 0 {
		return false
	}
	switch d.buf[0] {
	case wire.TagLiteral:
		if v1 > uint64(d.max)-uint64(len(d.out)) {
			d.fail(ErrOutputLimit, d.off)
			return false
		}
		if len(d.buf) < 1+k1+int(v1) {
			return false
		}
		start := len(d.out)
		d.out = append(d.out, d.buf[1+k1:1+k1+int(v1)]...)
		d.commit(start)
		d.consume(1 + k1 + int(v1))
	case wire.TagMatch, wire.TagEnd:
		v2, k2 := d.uvarint(1 + k1)
		if k2 == 0 {
			return false
		}
		if d.buf[0] == wire.TagMatch {
			if !d.match(v1, v2) {
				return false
			}
		} else {
			switch {
			case v1 != uint64(len(d.out)):
				d.fail(ErrLength, d.off)
			case uint32(v2) != d.crc:
				d.fail(ErrChecksum, d.off)
			}
			d.stage = 2
		}
		d.consume(1 + k1 + k2)
	default:
		d.fail(ErrBadTag, d.off)
	}
	return true
}

// uvarint reads a uvarint at d.buf[rel:]; k == 0 means more input is
// needed or the varint overflowed (then d.err is set).
func (d *Decoder) uvarint(rel int) (v uint64, k int) {
	v, k, ov := wire.ReadUvarint(d.buf[rel:])
	if ov {
		d.fail(ErrVarint, d.off+int64(rel))
		return 0, 0
	}
	return v, k
}

func (d *Decoder) match(dist, length uint64) bool {
	switch {
	case dist == 0:
		d.fail(ErrDistZero, d.off)
	case dist > uint64(d.win.Cap()):
		d.fail(ErrDistWindow, d.off)
	case dist > uint64(len(d.out)):
		d.fail(ErrDistHistory, d.off)
	case length > uint64(d.max)-uint64(len(d.out)):
		d.fail(ErrOutputLimit, d.off) // rejected before any output
	default:
		start := len(d.out)
		for rem := int64(length); rem > 0; {
			n := min(int64(dist), rem)
			src := len(d.out) - int(dist)
			d.out = append(d.out, d.out[src:src+int(n)]...)
			rem -= n
		}
		d.commit(start)
		return true
	}
	return false
}

func (d *Decoder) commit(start int) {
	d.win.AppendBytes(d.out[start:])
	d.crc = crc32.Update(d.crc, crc32.IEEETable, d.out[start:])
}

func (d *Decoder) consume(n int) {
	d.buf = d.buf[n:]
	d.off += int64(n)
}
