package stream

import (
	"errors"

	"ontology/b64"
)

var ErrLimit = errors.New("stream: output limit exceeded")

type Decoder struct {
	mime      bool
	limit     int64
	pending   []byte
	out       []byte
	prefix    int64
	checks    int64
	sawGroup  bool
	afterLine bool
	closed    bool
	failed    bool
}

func NewDecoder(mime bool, limit int64) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

func (d *Decoder) fail(kind b64.Kind, off int) error {
	d.failed = true
	return b64.Error{Kind: kind, Offset: off}
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.failed || d.closed {
		return 0, b64.Error{Kind: b64.KindInvalidChar, Offset: int(d.checks)}
	}
	for i, c := range p {
		d.checks++
		off := int(d.prefix) + i
		if len(d.pending) > 0 && d.pending[0] == '\r' {
			if c != '\n' {
				return i, d.fail(b64.KindNewline, int(d.prefix))
			}
			d.pending = d.pending[:0]
			i++
			continue
		}
		if c == '\r' {
			if !d.mime || !d.sawGroup || d.afterLine || len(d.pending) != 0 {
				return i, d.fail(b64.KindNewline, off)
			}
			if i+1 == len(p) {
				d.prefix += int64(i)
				d.afterLine = true
				d.pending = append(d.pending[:0], '\r')
				return len(p), nil
			}
			d.checks++
			if p[i+1] != '\n' {
				return i, d.fail(b64.KindNewline, off+1)
			}
			i++
			continue
		}
		if c == '\n' {
			if !d.mime || !d.sawGroup || d.afterLine || len(d.pending) != 0 {
				return i, d.fail(b64.KindNewline, off)
			}
			d.afterLine = true
			continue
		}
		d.pending = append(d.pending, c)
		if len(d.pending) == 4 {
			decoded, err := b64.Decode4(d.pending)
			if err != nil {
				var berr b64.Error
				errors.As(err, &berr)
				return i + 1, d.fail(berr.Kind, off-3+berr.Offset)
			}
			if d.limit >= 0 && int64(len(d.out)+len(decoded)) > d.limit {
				return i + 1, d.failEnd(ErrLimit)
			}
			d.out = append(d.out, decoded...)
			d.sawGroup, d.afterLine = true, false
			d.pending = d.pending[:0]
		}
	}
	n := len(p)
	if len(d.pending) > 0 && d.pending[0] == '\r' {
		d.prefix += int64(n) - 1
	} else {
		d.prefix += int64(n)
	}
	return n, nil
}

func (d *Decoder) failEnd(err error) error {
	d.failed = true
	return err
}

func (d *Decoder) Close() error {
	if d.failed {
		return b64.Error{Kind: b64.KindInvalidChar, Offset: int(d.checks)}
	}
	d.closed = true
	if len(d.pending) > 0 && d.pending[0] == '\r' {
		d.failed = true
		return b64.Error{Kind: b64.KindNewline, Offset: int(d.prefix)}
	}
	if len(d.pending) != 0 {
		d.failed = true
		return b64.Error{Kind: b64.KindLength, Offset: int(d.prefix)}
	}
	return nil
}

func (d *Decoder) Output() []byte { return append([]byte(nil), d.out...) }
func (d *Decoder) Checks() int64  { return d.checks }

type Encoder struct {
	mime    bool
	pending []byte
	out     []byte
	column  int
	closed  bool
}

func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) Write(p []byte) (int, error) {
	n := len(p)
	e.pending = append(e.pending, p...)
	for len(e.pending) >= 3 {
		e.emitGroup(b64.Encode3(e.pending[:3]))
		e.pending = append(e.pending[:0], e.pending[3:]...)
	}
	return n, nil
}

func (e *Encoder) emitGroup(group []byte) {
	for _, c := range group {
		if e.mime && e.column == 76 {
			e.out = append(e.out, '\r', '\n')
			e.column = 0
		}
		e.out = append(e.out, c)
		e.column++
	}
}

func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if len(e.pending) > 0 {
		e.emitGroup(b64.Encode3(e.pending))
	}
	return nil
}

func (e *Encoder) Output() []byte { return append([]byte(nil), e.out...) }
