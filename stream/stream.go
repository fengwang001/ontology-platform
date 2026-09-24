package stream

import (
	"errors"

	"ontology/b64"
)

var (
	ErrInvalidCharacter = errors.New("stream: invalid character")
	ErrNonCanonicalTail = errors.New("stream: non-canonical padded tail")
	ErrPaddingPosition  = errors.New("stream: padding in invalid position")
	ErrLength           = errors.New("stream: input length is not a multiple of four")
	ErrLineBreak        = errors.New("stream: line break in invalid position")
	ErrOutputLimit      = errors.New("stream: output limit exceeded")
	ErrClosed           = errors.New("stream: decoder is closed")
)

type OffsetError struct {
	Offset int64
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

type Decoder struct {
	mime       bool
	limit      int
	out        []byte
	group      [4]byte
	groupPos   int
	groupCount int
	lineGroups int
	padded     bool
	finalErr   error
	closed     bool
	finishErr  error
	offset     int64
	lastByte   int64
	checked    int64
	pendingCR  bool
}

func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

func (d *Decoder) Write(p []byte) (int, error) {
	for n, c := range p {
		d.checked++
		d.offset++
		d.lastByte = d.offset - 1
		if err := d.writeByte(c); err != nil {
			d.finishErr = err
			return n, err
		}
	}
	return len(p), nil
}

func (d *Decoder) writeByte(c byte) error {
	if d.finalErr != nil || d.closed {
		return d.at(ErrClosed)
	}
	if d.pendingCR {
		d.pendingCR = false
		if c != '\n' {
			d.finalErr = d.at(ErrLineBreak)
			return d.finalErr
		}
		return d.consumeLineBreak()
	}
	if c == '\r' {
		if !d.mime || d.groupPos != 0 || d.padded || d.lineGroups == 0 || d.lineGroups > 19 {
			d.finalErr = d.at(ErrLineBreak)
			return d.finalErr
		}
		d.pendingCR = true
		return nil
	}
	if c == '\n' {
		if !d.mime || d.groupPos != 0 || d.padded || d.lineGroups == 0 || d.lineGroups > 19 {
			d.finalErr = d.at(ErrLineBreak)
			return d.finalErr
		}
		return d.consumeLineBreak()
	}
	if d.padded {
		d.finalErr = d.at(ErrPaddingPosition)
		return d.finalErr
	}
	d.group[d.groupPos] = c
	d.groupPos++
	if d.groupPos == 4 {
		return d.finishGroup()
	}
	return nil
}

func (d *Decoder) consumeLineBreak() error {
	d.lineGroups = 0
	return nil
}

func (d *Decoder) finishGroup() error {
	decoded, err := b64.DecodeQuartet(d.group)
	if err != nil {
		d.finalErr = d.atOffset(d.lastByte, mapError(err))
		return d.finalErr
	}
	if d.limit >= 0 && len(d.out)+len(decoded) > d.limit {
		d.finalErr = d.atOffset(d.lastByte, ErrOutputLimit)
		d.closed = true
		return d.finalErr
	}
	d.out = append(d.out, decoded...)
	d.groupPos = 0
	d.groupCount++
	if b64.IsPadding(d.group) {
		d.padded = true
	}
	d.lineGroups++
	return nil
}

func (d *Decoder) at(err error) error { return &OffsetError{Offset: d.offset - 1, Err: err} }

func (d *Decoder) atOffset(offset int64, err error) *OffsetError {
	return &OffsetError{Offset: offset, Err: err}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, b64.ErrInvalidCharacter):
		return ErrInvalidCharacter
	case errors.Is(err, b64.ErrNonCanonicalTail):
		return ErrNonCanonicalTail
	default:
		return ErrPaddingPosition
	}
}

func (d *Decoder) Close() error {
	if d.closed {
		if d.finishErr != nil {
			return d.finishErr
		}
		return ErrClosed
	}
	d.closed = true
	if d.finalErr != nil {
		d.finishErr = d.finalErr
	}
	if d.finishErr == nil && (d.pendingCR || d.groupPos != 0) {
		offset := d.lastByte
		if d.pendingCR {
			offset = d.lastByte
		}
		d.finishErr = d.atOffset(offset, ErrLength)
	}
	return d.finishErr
}

func (d *Decoder) Output() []byte {
	out := make([]byte, len(d.out))
	copy(out, d.out)
	return out
}

func (d *Decoder) CheckedBytes() int64 { return d.checked }

type Encoder struct {
	mime   bool
	prefix [3]byte
	have   int
	cols   int
	out    []byte
}

func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) Write(p []byte) (int, error) {
	for _, c := range p {
		e.prefix[e.have] = c
		e.have++
		if e.have == 3 {
			e.writeTriplet(e.prefix[:])
			e.have = 0
		}
	}
	return len(p), nil
}

func (e *Encoder) writeTriplet(t []byte) {
	q := b64.EncodeTriplet(t)
	for _, c := range q {
		if e.mime && e.cols > 0 && e.cols%76 == 0 {
			e.out = append(e.out, '\r', '\n')
			e.cols = 0
		}
		e.out = append(e.out, c)
		e.cols++
	}
}

func (e *Encoder) Close() error {
	if e.have > 0 {
		e.writeTriplet(e.prefix[:e.have])
		e.have = 0
	}
	return nil
}

func (e *Encoder) Output() []byte {
	out := make([]byte, len(e.out))
	copy(out, e.out)
	return out
}

func Encode(p []byte, mime bool) []byte {
	e := NewEncoder(mime)
	_, _ = e.Write(p)
	_ = e.Close()
	return e.Output()
}

func Decode(p []byte, mime bool, limit int) ([]byte, error) {
	d := NewDecoder(mime, limit)
	_, err := d.Write(p)
	if err == nil {
		err = d.Close()
	}
	return d.Output(), err
}
