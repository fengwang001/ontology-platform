package stream

import (
	"errors"

	"ontology/b64"
)

var (
	ErrInvalidCharacter = errors.New("stream: invalid character")
	ErrNonCanonicalTail = errors.New("stream: non-canonical tail")
	ErrPaddingPosition  = errors.New("stream: padding position")
	ErrInvalidLength    = errors.New("stream: input length is not a multiple of four")
	ErrInvalidNewline   = errors.New("stream: newline position")
	ErrOutputLimit      = errors.New("stream: output limit reached")
	ErrClosed           = errors.New("stream: closed")
)

type Error struct {
	Op     string
	Offset int64
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Decoder struct {
	mime      bool
	limit     int
	out       []byte
	group     [4]byte
	groupPos  int
	offset    int64
	checked   int64
	pendingCR bool
	finished  bool
	closed    bool
	fatal     *Error
}

func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

func (d *Decoder) fail(offset int64, err error) error {
	d.fatal = &Error{Op: "decode", Offset: offset, Err: err}
	return d.fatal
}

func (d *Decoder) emitGroup() error {
	decoded, count, err := b64.DecodeChunk(d.group)
	if err != nil {
		offset := d.offset - 1
		switch {
		case errors.Is(err, b64.ErrNonCanonicalTail):
			offset = d.offset - 3
			if d.group[2] != '=' {
				offset = d.offset - 2
			}
			err = ErrNonCanonicalTail
		case errors.Is(err, b64.ErrPaddingPosition):
			err = ErrPaddingPosition
		default:
			err = ErrInvalidCharacter
		}
		return d.fail(offset, err)
	}
	if d.limit >= 0 && len(d.out)+count > d.limit {
		return d.fail(d.offset-1, ErrOutputLimit)
	}
	d.out = append(d.out, decoded[:count]...)
	d.finished = d.group[2] == '=' || d.group[3] == '='
	d.groupPos = 0
	return nil
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.closed {
		return 0, ErrClosed
	}
	if d.fatal != nil {
		return 0, d.fatal
	}
	for i, char := range p {
		d.checked++
		offset := d.offset
		d.offset++
		if char == '\r' {
			d.pendingCR = false
			if !d.mime || d.groupPos != 0 || d.finished {
				return i, d.fail(offset, ErrInvalidNewline)
			}
			d.pendingCR = true
			continue
		}
		if char == '\n' {
			crlf := d.pendingCR
			d.pendingCR = false
			if (!crlf && (!d.mime || d.groupPos != 0 || d.finished)) || d.groupPos != 0 {
				return i, d.fail(offset, ErrInvalidNewline)
			}
			continue
		}
		if d.pendingCR {
			d.pendingCR = false
			return i, d.fail(offset-1, ErrInvalidNewline)
		}
		if char == '=' {
			if d.finished || d.groupPos < 2 {
				return i, d.fail(offset, ErrPaddingPosition)
			}
		} else if d.finished || (d.groupPos == 3 && d.group[2] == '=') {
			return i, d.fail(offset, ErrPaddingPosition)
		} else if !b64.ValidChar(char) {
			return i, d.fail(offset, ErrInvalidCharacter)
		}
		d.group[d.groupPos] = char
		d.groupPos++
		if d.groupPos == 4 && d.emitGroup() != nil {
			return i, d.fatal
		}
	}
	return len(p), nil
}

func (d *Decoder) Close() error {
	if d.fatal != nil {
		return d.fatal
	}
	if d.closed {
		return nil
	}
	d.closed = true
	switch {
	case d.pendingCR:
		return d.fail(d.offset-1, ErrInvalidNewline)
	case d.groupPos != 0:
		return d.fail(d.offset, ErrInvalidLength)
	default:
		return nil
	}
}

func (d *Decoder) Output() []byte      { return append([]byte(nil), d.out...) }
func (d *Decoder) checkedCount() int64 { return d.checked }

type Encoder struct {
	mime      bool
	buf       [3]byte
	n         int
	lineChars int
	out       []byte
	closed    bool
}

func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) emit(in []byte) {
	if e.mime && e.lineChars == 76 {
		e.out = append(e.out, '\r', '\n')
		e.lineChars = 0
	}
	group, _ := b64.EncodeChunk(in)
	e.out = append(e.out, group[:]...)
	e.lineChars += 4
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	for _, char := range p {
		e.buf[e.n] = char
		e.n++
		if e.n == cap(e.buf) {
			e.emit(e.buf[:cap(e.buf)])
			e.n = 0
			e.buf = [3]byte{}
		}
	}
	return len(p), nil
}

func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if e.n > 0 {
		e.emit(e.buf[:e.n])
	}
	return nil
}

func (e *Encoder) Output() []byte { return append([]byte(nil), e.out...) }
