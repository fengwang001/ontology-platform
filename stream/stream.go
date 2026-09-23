package stream

import (
	"errors"

	"ontology/b64"
)

var ErrLimitExceeded = errors.New("decoded output limit exceeded")

type Decoder struct {
	mime    bool
	limit   int
	buf     [4]byte
	used    int
	out     []byte
	checked int64
	err     error
	pad     bool
	cr      bool
	crAt    int
}

type Encoder struct {
	mime bool
	cols  int
	out  []byte
}

type Option func(*Decoder)

func NewDecoder(opts ...Option) *Decoder {
	d := &Decoder{limit: -1}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func MIME() Option {
	return func(d *Decoder) { d.mime = true }
}

func WithLimit(n int) Option {
	return func(d *Decoder) { d.limit = n }
}

func (d *Decoder) Write(p []byte) (int, error) {
	for _, b := range p {
		offset := int(d.checked)
		d.checked++
		if d.err != nil {
			return len(p), d.err
		}
		if d.cr {
			if b != '\n' {
				d.fail(d.crAt, b64.ErrNewlinePosition)
				return len(p), d.err
			}
			d.cr = false
			continue
		}
		if b == '\r' {
			if !d.mime || d.used != 0 || d.pad {
				d.fail(offset, b64.ErrNewlinePosition)
			} else {
				d.cr, d.crAt = true, offset
			}
			continue
		}
		if b == '\n' {
			if !d.mime || d.used != 0 || d.pad {
				d.fail(offset, b64.ErrNewlinePosition)
			}
			continue
		}
		if d.pad {
			d.fail(offset, b64.ErrPaddingPosition)
			continue
		}
		if b != '=' && !validByte(b) {
			d.fail(offset, b64.ErrInvalidChar)
			continue
		}
		d.buf[d.used] = b
		d.used++
		if d.used == 4 {
			d.finishGroup(offset - 3)
		}
	}
	return len(p), d.err
}

func (d *Decoder) Close() error {
	if d.err == nil {
		switch {
		case d.cr:
			d.fail(d.crAt, b64.ErrNewlinePosition)
		case d.used != 0:
			d.fail(int(d.checked), b64.ErrLength)
		}
	}
	return d.err
}

func (d *Decoder) Output() []byte { return d.out }
func (d *Decoder) Checked() int64 { return d.checked }

func (d *Decoder) finishGroup(offset int) {
	decoded, err := b64.DecodeGroup(d.buf, offset)
	if err != nil {
		d.err = err
		return
	}
	if d.limit >= 0 && len(d.out)+len(decoded) > d.limit {
		d.fail(offset, ErrLimitExceeded)
		return
	}
	d.out = append(d.out, decoded...)
	d.pad = d.buf[2] == '=' || d.buf[3] == '='
	d.used = 0
}

func (d *Decoder) fail(offset int, kind error) {
	d.err = &b64.GroupError{Offset: offset, Kind: kind}
}

func validByte(b byte) bool {
	return b == '+' || b == '/' ||
		('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z') || ('0' <= b && b <= '9')
}

func (e *Encoder) Write(p []byte) (int, error) { return len(p), nil }
func (e *Encoder) Close() error { return nil }
func (e *Encoder) Output() []byte { return e.out }

func Encode(src []byte, mime bool) []byte { return nil }
func Decode(src []byte, opts ...Option) ([]byte, error) { return nil, nil }
