package dec

import (
	"bytes"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"

	"ontology/window"
	"ontology/wire"
)

var (
	ErrBadHeader    = errors.New("dec: bad header")
	ErrBadTag       = errors.New("dec: unknown tag")
	ErrZeroDist     = errors.New("dec: zero distance")
	ErrPastHistory  = errors.New("dec: distance beyond history")
	ErrPastWindow   = errors.New("dec: distance beyond window")
	ErrBadChecksum  = errors.New("dec: checksum mismatch")
	ErrBadLength    = errors.New("dec: original length mismatch")
	ErrTrailingData = errors.New("dec: trailing data")
	ErrLimit        = errors.New("dec: output limit exceeded")
)

type DecodeError struct {
	Offset int64
	Err    error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%v at offset %d", e.Err, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

type Config struct {
	Window    int
	MaxOutput uint64
}

type Decoder struct {
	in       bytes.Buffer
	out      []byte
	win      *window.Window
	off      int64
	history  uint64
	limit    uint64
	ended    bool
	terminal error
	head     bool
	sum      hash.Hash64
}

func New(c Config) (*Decoder, error) {
	if c.Window == 0 {
		c.Window = 65536
	}
	win, err := window.New(c.Window)
	if err != nil {
		return nil, errors.New("dec: invalid configuration")
	}
	return &Decoder{win: win, limit: c.MaxOutput, sum: fnv.New64()}, nil
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.terminal != nil {
		return 0, d.terminal
	}
	n := len(p)
	d.in.Write(p)
	d.terminal = d.parse()
	return n, d.terminal
}

func (d *Decoder) Close() error {
	if d.terminal != nil {
		return d.terminal
	}
	if !d.head || d.in.Len() > 0 || !d.ended {
		return &DecodeError{Offset: d.off + int64(d.in.Len()), Err: wire.ErrTruncated}
	}
	return nil
}

func (d *Decoder) Output() []byte { return d.out }

func (d *Decoder) parse() error {
	all := d.in.Bytes()
	p := all
	if !d.head {
		h := wire.Header()
		if len(p) < len(h) {
			return nil
		}
		if !bytes.Equal(p[:len(h)], h) {
			return &DecodeError{Offset: int64(firstDiff(p, h)), Err: ErrBadHeader}
		}
		p, d.off, d.head = p[len(h):], int64(len(h)), true
	}
	for len(p) > 0 {
		if d.ended {
			return d.err(0, ErrTrailingData)
		}
		tagOff, tag := d.off, p[0]
		p, d.off = p[1:], d.off+1
		switch tag {
		case wire.TagFlush:
		case wire.TagLiteral:
			n, err := d.varint(&p)
			if err != nil {
				return err
			}
			if n == 0 {
				return &DecodeError{Offset: tagOff, Err: wire.ErrBadLength}
			}
			if uint64(len(p)) < n {
				d.keep(all, p)
				return nil
			}
			if err := d.addBytes(p[:n]); err != nil {
				return &DecodeError{Offset: tagOff, Err: err}
			}
			p, d.off = p[n:], d.off+int64(n)
		case wire.TagMatch:
			dist, err := d.varint(&p)
			if err != nil {
				return err
			}
			length, err := d.varint(&p)
			if err != nil {
				return err
			}
			if dist == 0 || length == 0 {
				return &DecodeError{Offset: tagOff, Err: ErrZeroDist}
			}
			if dist > d.history {
				return &DecodeError{Offset: tagOff, Err: ErrPastHistory}
			}
			if dist > uint64(d.win.Capacity()) {
				return &DecodeError{Offset: tagOff, Err: ErrPastWindow}
			}
			if d.limit > 0 && d.history+length > d.limit {
				return &DecodeError{Offset: tagOff, Err: ErrLimit}
			}
			if err := d.addCopy(dist, length); err != nil {
				return err
			}
		case wire.TagEnd:
			total, err := d.varint(&p)
			if err != nil {
				return err
			}
			checksum, err := d.varint(&p)
			if err != nil {
				return err
			}
			if total != d.history {
				return &DecodeError{Offset: tagOff, Err: ErrBadLength}
			}
			if checksum != d.sum.Sum64() {
				return &DecodeError{Offset: tagOff, Err: ErrBadChecksum}
			}
			d.ended = true
		default:
			return &DecodeError{Offset: tagOff, Err: ErrBadTag}
		}
	}
	d.keep(all, p)
	return nil
}

func (d *Decoder) varint(p *[]byte) (uint64, error) {
	v, n, err := wire.Uvarint(*p)
	if err != nil {
		return 0, &DecodeError{Offset: d.off, Err: err}
	}
	*p, d.off = (*p)[n:], d.off+int64(n)
	return v, nil
}

func (d *Decoder) keep(all, p []byte) {
	d.in.Reset()
	d.in.Write(p)
}

func (d *Decoder) addBytes(p []byte) error {
	d.out = append(d.out, p...)
	for _, b := range p {
		d.win.Add(b)
	}
	d.history += uint64(len(p))
	_, _ = d.sum.Write(p)
	return nil
}

func (d *Decoder) addCopy(distance, length uint64) error {
	for i := uint64(0); i < length; i++ {
		b := d.win.At(int(distance))
		d.out = append(d.out, b)
		d.win.Add(b)
		_, _ = d.sum.Write([]byte{b})
	}
	d.history += length
	return nil
}

func (d *Decoder) err(_ int64, err error) error {
	e := &DecodeError{Offset: d.off, Err: err}
	d.terminal = e
	return e
}

func firstDiff(p, h []byte) int {
	for i := range h {
		if i >= len(p) || p[i] != h[i] {
			return i
		}
	}
	return 0
}
