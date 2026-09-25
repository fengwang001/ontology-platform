// Package dec implements a streaming decompressor for the wire format.
// It validates the stream byte by byte and reports distinguishable errors
// carrying the compressed-stream offset. See DESIGN.md for the format.
package dec

import (
	"errors"
	"fmt"

	"ontology/window"
	"ontology/wire"
)

// Distinguishable stream-corruption sentinels (wrapped by Error).
var (
	ErrBadMagic         = errors.New("bad stream magic")
	ErrBadVersion       = errors.New("unsupported stream version")
	ErrBadTag           = errors.New("unknown record tag")
	ErrZeroDistance     = errors.New("backref distance is zero")
	ErrDistanceTooLarge = errors.New("backref distance exceeds window capacity")
	ErrDistanceTooFar   = errors.New("backref distance exceeds bytes already output")
	ErrLengthMismatch   = errors.New("end record length mismatch")
	ErrChecksumMismatch = errors.New("end record checksum mismatch")
	ErrTrailingData     = errors.New("bytes after end of stream")
	ErrTruncated        = errors.New("truncated stream")
	ErrOutputLimit      = errors.New("output limit exceeded")
	ErrClosed           = errors.New("decompressor already closed")
)

// Error is a decode failure at a compressed-stream byte offset.
type Error struct {
	Kind error
	Off  int
}

func (e *Error) Error() string { return fmt.Sprintf("dec: offset %d: %v", e.Off, e.Kind) }
func (e *Error) Unwrap() error { return e.Kind }

// Config tunes a Decompressor; zero fields select defaults.
type Config struct {
	WindowCap int
	MaxOutput int
}

// Decompressor consumes a compressed stream incrementally.
// It is not safe for concurrent use.
type Decompressor struct {
	w       *window.Window
	max     int
	buf     []byte
	off     int
	started bool
	done    bool
	closed  bool
	out     []byte
	sum     wire.Sum
	err     error
}

// New creates a Decompressor; cfg may be nil for defaults.
func New(cfg *Config) (*Decompressor, error) {
	c := Config{WindowCap: 1 << 16, MaxOutput: 1 << 30}
	if cfg != nil {
		if cfg.WindowCap != 0 {
			c.WindowCap = cfg.WindowCap
		}
		if cfg.MaxOutput != 0 {
			c.MaxOutput = cfg.MaxOutput
		}
	}
	w, err := window.New(c.WindowCap)
	if err != nil {
		return nil, err
	}
	return &Decompressor{w: w, max: c.MaxOutput, sum: wire.NewSum()}, nil
}

func (d *Decompressor) fail(kind error, off int) {
	d.err = &Error{Kind: kind, Off: off}
}

func (d *Decompressor) advance(n int) {
	d.buf = d.buf[n:]
	d.off += n
}

// Write feeds compressed bytes and parses every complete record.
func (d *Decompressor) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if d.closed {
		return 0, ErrClosed
	}
	d.buf = append(d.buf, p...)
	d.parse()
	if d.err != nil {
		return 0, d.err
	}
	return len(p), nil
}

func (d *Decompressor) parse() {
	for d.err == nil {
		if d.done {
			if len(d.buf) > 0 {
				d.fail(ErrTrailingData, d.off)
			}
			return
		}
		if !d.started {
			if len(d.buf) < wire.HeaderLen {
				return
			}
			if d.buf[0] != wire.Magic0 || d.buf[1] != wire.Magic1 || d.buf[2] != wire.Magic2 {
				d.fail(ErrBadMagic, d.off)
				return
			}
			if d.buf[3] != wire.Version {
				d.fail(ErrBadVersion, d.off+3)
				return
			}
			d.advance(wire.HeaderLen)
			d.started = true
			continue
		}
		if len(d.buf) == 0 {
			return
		}
		if !d.record() {
			return
		}
	}
}

// record parses and applies one record; false means more input is needed.
func (d *Decompressor) record() bool {
	start := d.off
	switch d.buf[0] {
	case wire.TagFlush:
		d.advance(1)
	case wire.TagLiteral:
		n, k, err := wire.Uvarint(d.buf[1:])
		if err != nil {
			d.fail(err, start+1)
		} else if k == 0 {
			return false
		} else if n > uint64(d.max-d.w.Total()) {
			d.fail(ErrOutputLimit, start)
		} else if len(d.buf) < 1+k+int(n) {
			return false
		} else {
			lit := d.buf[1+k : 1+k+int(n)]
			d.w.Write(lit)
			d.sum.AddBytes(lit)
			d.out = append(d.out, lit...)
			d.advance(1 + k + int(n))
		}
	case wire.TagBackref:
		dist, k1, err := wire.Uvarint(d.buf[1:])
		if err != nil {
			d.fail(err, start+1)
			break
		}
		if k1 == 0 {
			return false
		}
		length, k2, err := wire.Uvarint(d.buf[1+k1:])
		if err != nil {
			d.fail(err, start+1+k1)
			break
		}
		if k2 == 0 {
			return false
		}
		switch total := d.w.Total(); {
		case dist == 0:
			d.fail(ErrZeroDistance, start)
		case dist > uint64(d.w.Cap()):
			d.fail(ErrDistanceTooLarge, start)
		case dist > uint64(total):
			d.fail(ErrDistanceTooFar, start)
		case length > uint64(d.max-total):
			d.fail(ErrOutputLimit, start)
		default:
			for i := 0; i < int(length); i++ {
				b := d.w.Back(int(dist))
				d.w.Put(b)
				d.sum.Add(b)
				d.out = append(d.out, b)
			}
			d.advance(1 + k1 + k2)
		}
	case wire.TagEnd:
		total, k1, err := wire.Uvarint(d.buf[1:])
		if err != nil {
			d.fail(err, start+1)
			break
		}
		if k1 == 0 {
			return false
		}
		sum, k2, err := wire.Uvarint(d.buf[1+k1:])
		if err != nil {
			d.fail(err, start+1+k1)
			break
		}
		if k2 == 0 {
			return false
		}
		switch {
		case total != uint64(d.w.Total()):
			d.fail(ErrLengthMismatch, start)
		case sum != d.sum.Value():
			d.fail(ErrChecksumMismatch, start)
		default:
			d.advance(1 + k1 + k2)
			d.done = true
		}
	default:
		d.fail(ErrBadTag, start)
	}
	return d.err == nil
}

// Close reports the stream as complete, or ErrTruncated otherwise.
func (d *Decompressor) Close() error {
	if d.err != nil {
		return d.err
	}
	d.closed = true
	if !d.done {
		d.err = &Error{Kind: ErrTruncated, Off: d.off}
	}
	return d.err
}

// Output returns a copy of everything decompressed so far.
func (d *Decompressor) Output() []byte {
	return append([]byte(nil), d.out...)
}
