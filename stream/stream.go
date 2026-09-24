package stream

import (
	"errors"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Format int

const (
	UTF8 Format = iota
	UTF16LE
	UTF16BE
)

type Options struct {
	From, To        Format
	Strict, EmitBOM bool
	MaxOutput       int
}
type Stats struct{ Consumed, Scalars, Invalid, InvalidBytes, BOMBytes, Checks int64 }

var (
	ErrIllegal   = errors.New("illegal encoding unit")
	ErrTruncated = errors.New("truncated input")
	ErrLimit     = errors.New("output limit exceeded")
	ErrClosed    = errors.New("transformer is in terminal state")
)

type UnitError struct {
	Kind   error
	Offset int64
	Length int
}

func (e *UnitError) Error() string { return e.Kind.Error() }
func (e *UnitError) Unwrap() error { return e.Kind }

type Transformer struct {
	opts       Options
	out, carry []byte
	abs, start int64
	stats      Stats
	terminal   error
	bom        bool
	d8         *u8.Decoder
	d16        *u16.Decoder
}

func New(o Options) *Transformer {
	e := u16.Little
	if o.From == UTF16BE || o.To == UTF16BE {
		e = u16.Big
	}
	t := &Transformer{opts: o, d8: u8.NewDecoder(), d16: u16.NewDecoder(e)}
	if o.EmitBOM {
		t.out = append(t.out, t.encode(0xFEFF)...)
	}
	return t
}

func (t *Transformer) Write(p []byte) (int, error) {
	if t.terminal != nil {
		return 0, t.terminal
	}
	data := append(append([]byte(nil), t.carry...), p...)
	t.carry, t.stats.Consumed = nil, 0
	for i := 0; i < len(data); {
		if !t.inUnit() {
			t.start = t.abs + int64(i)
		}
		t.stats.Checks++
		u, done, used := t.feed(data[i])
		if !done {
			i++
			continue
		}
		if err := t.unit(u, used); err != nil {
			t.carry, t.terminal = append(t.carry, data[i:]...), err
			return int(t.start), err
		}
		i++
	}
	t.carry = append(t.carry, t.pending()...)
	t.stats.Consumed = t.abs + int64(len(data)-len(t.carry))
	n := len(p) - len(t.carry)
	t.abs = t.stats.Consumed
	return n, nil
}

func (t *Transformer) Close() error {
	if t.terminal != nil {
		return t.terminal
	}
	if n := len(t.pending()); n > 0 {
		t.stats.Checks++
		t.stats.Invalid++
		t.stats.InvalidBytes += int64(n)
		if t.opts.Strict {
			t.terminal = &UnitError{Kind: ErrTruncated, Offset: t.start, Length: n}
			return t.terminal
		}
		if err := t.put(scalar.Replacement); err != nil {
			t.terminal = err
			return err
		}
		t.stats.Scalars++
	}
	t.terminal = ErrClosed
	return nil
}

func (t *Transformer) Output() []byte { return append([]byte(nil), t.out...) }
func (t *Transformer) Stats() Stats   { return t.stats }

func (t *Transformer) unit(u u8.Unit, used bool) error {
	n := u.Size
	if !used {
		n = 0
	}
	if u.Legal && u.R == 0xFEFF && !t.bom && t.start == 0 {
		t.bom, t.stats.BOMBytes = true, t.stats.BOMBytes+int64(u.Size)
		return nil
	}
	if !u.Legal {
		t.stats.Invalid++
		t.stats.InvalidBytes += int64(n)
		if t.opts.Strict {
			return &UnitError{Kind: ErrIllegal, Offset: t.start, Length: n}
		}
		u.R, u.Legal = scalar.Replacement, true
	}
	if err := t.put(u.R); err != nil {
		return err
	}
	t.stats.Scalars++
	return nil
}

func (t *Transformer) put(r rune) error {
	b := t.encode(r)
	if t.opts.MaxOutput > 0 && len(t.out)+len(b) > t.opts.MaxOutput {
		return ErrLimit
	}
	t.out = append(t.out, b...)
	return nil
}

func (t *Transformer) encode(r rune) []byte {
	if t.opts.To == UTF8 {
		return u8.Encode(r)
	}
	e := u16.Little
	if t.opts.To == UTF16BE {
		e = u16.Big
	}
	return u16.Encode(r, e)
}

func (t *Transformer) feed(b byte) (u8.Unit, bool, bool) {
	if t.opts.From == UTF8 {
		return t.d8.Feed(b)
	}
	u, done, used := t.d16.Feed(b)
	return u8.Unit{R: u.R, Size: u.Size, Legal: u.Legal, Partial: u.Partial}, done, used
}
func (t *Transformer) pending() []byte {
	if t.opts.From == UTF8 {
		return t.d8.Pending()
	}
	return t.d16.Pending()
}
func (t *Transformer) inUnit() bool {
	if t.opts.From == UTF8 {
		return t.d8.InUnit()
	}
	return t.d16.InUnit()
}
