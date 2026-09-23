package stream

import (
	"errors"

	"ontology/b64"
)

var (
	ErrInvalidChar      = errors.New("stream: invalid character")
	ErrNonCanonicalTail = errors.New("stream: non-canonical tail")
	ErrInvalidPadding   = errors.New("stream: invalid padding")
	ErrInvalidLength    = errors.New("stream: input length is not a multiple of four")
	ErrInvalidNewline   = errors.New("stream: newline at invalid position")
	ErrLimit            = errors.New("stream: output limit exceeded")
	ErrClosed           = errors.New("stream: decoder is closed")
)

type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

type Decoder struct {
	mime       bool
	limit      int
	output     []byte
	checked    int
	closed     bool
	final      bool
	group      [4]byte
	groupStart int
	size       int
	pendingCR  bool
	terminal   error
}

func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.closed {
		return 0, ErrClosed
	}
	if d.terminal != nil {
		return 0, d.terminal
	}
	for i, char := range p {
		offset := d.checked
		d.checked++
		if d.pendingCR {
			d.pendingCR = false
			if char != '\n' || !d.mime || d.size != 0 {
				return i + 1, d.fail(offset-1, ErrInvalidNewline)
			}
			continue
		}
		if char == '\r' || char == '\n' {
			if !d.mime || d.size != 0 || d.final {
				return i + 1, d.fail(offset, ErrInvalidNewline)
			}
			d.pendingCR = char == '\r'
			continue
		}
		if d.final {
			return i + 1, d.fail(offset, ErrInvalidPadding)
		}
		if d.size == 0 {
			d.groupStart = offset
		}
		d.group[d.size] = char
		d.size++
		if d.size == 4 {
			if err := d.finishGroup(); err != nil {
				return i + 1, err
			}
		}
	}
	return len(p), nil
}

func (d *Decoder) Close() error {
	if d.closed {
		return ErrClosed
	}
	d.closed = true
	if d.terminal != nil {
		return d.terminal
	}
	if d.pendingCR {
		return d.fail(d.checked-1, ErrInvalidNewline)
	}
	if d.size != 0 {
		return d.fail(d.groupStart, ErrInvalidLength)
	}
	return nil
}

func (d *Decoder) Output() []byte    { return append([]byte(nil), d.output...) }
func (d *Decoder) checkedBytes() int { return d.checked }

func (d *Decoder) finishGroup() error {
	decoded, err := b64.DecodeGroup(d.group[:])
	if err != nil {
		var groupErr *b64.GroupError
		if errors.As(err, &groupErr) {
			mapped := ErrInvalidChar
			switch {
			case errors.Is(groupErr.Err, b64.ErrInvalidPadding):
				mapped = ErrInvalidPadding
			case errors.Is(groupErr.Err, b64.ErrNonCanonicalTail):
				mapped = ErrNonCanonicalTail
			}
			return d.fail(d.groupStart+groupErr.Offset, mapped)
		}
		return d.fail(d.groupStart, ErrInvalidChar)
	}
	if d.limit >= 0 && len(d.output)+len(decoded) > d.limit {
		d.final = true
		return d.fail(d.groupStart, ErrLimit)
	}
	d.output = append(d.output, decoded...)
	if d.group[2] == '=' || d.group[3] == '=' {
		d.final = true
	}
	d.size = 0
	return nil
}

func (d *Decoder) fail(offset int, err error) error {
	d.final = true
	d.terminal = &OffsetError{Offset: offset, Err: err}
	return d.terminal
}

type Encoder struct {
	mime      bool
	output    []byte
	pending   []byte
	column    int
	closed    bool
}

func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	e.pending = append(e.pending, p...)
	for len(e.pending) >= 3 {
		e.writeGroup(e.pending[:3])
		e.pending = append([]byte(nil), e.pending[3:]...)
	}
	return len(p), nil
}

func (e *Encoder) Close() error {
	if e.closed {
		return ErrClosed
	}
	e.closed = true
	if len(e.pending) > 0 {
		e.writeGroup(e.pending)
		e.pending = nil
	}
	return nil
}

func (e *Encoder) Output() []byte { return append([]byte(nil), e.output...) }

func (e *Encoder) writeGroup(group []byte) {
	if e.mime && e.column == 76 {
		e.output = append(e.output, '\r', '\n')
		e.column = 0
	}
	encoded := b64.EncodeGroup(group)
	e.output = append(e.output, encoded...)
	e.column += len(encoded)
}

func Encode(input []byte, mime bool) []byte {
	encoder := NewEncoder(mime)
	_, _ = encoder.Write(input)
	_ = encoder.Close()
	return encoder.Output()
}

func Decode(input []byte, mime bool, limit int) ([]byte, error) {
	decoder := NewDecoder(mime, limit)
	if _, err := decoder.Write(input); err != nil {
		return decoder.Output(), err
	}
	if err := decoder.Close(); err != nil {
		return decoder.Output(), err
	}
	return decoder.Output(), nil
}

var _ = b64.Alphabet
