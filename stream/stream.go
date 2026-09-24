package stream

import (
	"errors"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Format int
type Mode int

const (
	UTF8 Format = iota
	UTF16LE
	UTF16BE
	UTF16BOM
)
const (
	Replace Mode = iota
	Strict
)

var (
	ErrInvalid     = errors.New("invalid unicode unit")
	ErrTruncated   = errors.New("truncated unicode input")
	ErrOutputLimit = errors.New("output limit exceeded")
	ErrClosed      = errors.New("transcoder closed")
)

type UnitError struct {
	Kind   error
	Offset int64
	Length int
}

func (e *UnitError) Error() string { return e.Kind.Error() }
func (e *UnitError) Unwrap() error { return e.Kind }

type Stats struct{ Scalars, Invalid, InvalidBytes, BOMBytes, Consumed, Checks int64 }
type Config struct {
	From, To Format
	Mode     Mode
	Limit    int
	EmitBOM  bool
}
type ev struct {
	r                           scalar.Value
	n                           int
	ok, retry, wait, bom, trunc bool
}
type Transcoder struct {
	c                   Config
	b                   []byte
	s                   Stats
	pos, off, held      int64
	d8                  u8.Decoder
	d16                 *u16.Decoder
	closed, dead, first bool
	err                 error
	warm, cut           int64
}

func New(c Config) *Transcoder {
	e := u16.Little
	if c.From == UTF16BE {
		e = u16.Big
	}
	if c.From == UTF16BOM {
		e = u16.BOM
	}
	return &Transcoder{c: c, d16: u16.NewDecoder(e), first: true, warm: -1}
}
func Segment(c Config, prefix int) *Transcoder {
	t := New(c)
	t.warm, t.cut = 1, int64(prefix)
	t.first = false
	t.d16.SkipBOM()
	return t
}
func (t *Transcoder) Output() []byte { return append([]byte(nil), t.b...) }
func (t *Transcoder) Stats() Stats   { x := t.s; x.Checks += t.d8.Checks() + t.d16.Checks(); return x }
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.dead || t.closed {
		return 0, t.err
	}
	n := 0
	for len(p) > 0 {
		if t.c.Limit > 0 && len(t.b)+t.need() > t.c.Limit {
			t.err = ErrOutputLimit
			return n, t.err
		}
		t.off = t.pos + t.held
		start := t.pos
		x := t.feed(p)
		a := x.n
		if x.wait {
			a = len(p)
		}
		if x.retry {
			a = 0
		}
		if a > len(p) {
			a = len(p)
		}
		t.pos += int64(a)
		p = p[a:]
		if x.wait {
			t.held = int64(t.pending())
			return n, nil
		}
		pref := t.warm >= 0 && start < t.cut
		if pref && start+int64(x.n) <= t.cut {
			continue
		}
		if t.warm == 1 && start < t.cut {
			t.warm = 0
		}
		if t.warm == 0 {
			t.warm = -1
		}
		if !pref {
			n += a
			t.s.Consumed += int64(a)
		}
		if e := t.emit(x, pref); e != nil {
			t.dead = true
			t.err = &UnitError{Kind: e, Offset: t.off, Length: x.n}
			return n, t.err
		}
	}
	return n, nil
}
func (t *Transcoder) Close() error {
	if t.dead {
		return t.err
	}
	if t.closed {
		return ErrClosed
	}
	t.closed = true
	x := t.finish()
	if x.n == 0 {
		return nil
	}
	t.off = t.pos
	if e := t.emit(x, false); e != nil {
		t.dead = true
		t.err = &UnitError{Kind: e, Offset: t.off, Length: x.n}
		return t.err
	}
	t.s.Consumed += int64(x.n)
	return nil
}
func (t *Transcoder) need() int {
	if t.c.To == UTF8 || t.d16.Pending() != 2 {
		return 4
	}
	return 2
}
func (t *Transcoder) pending() int {
	if t.c.From == UTF8 {
		return t.d8.Pending()
	}
	return t.d16.Pending()
}
func (t *Transcoder) feed(p []byte) ev {
	if t.c.From == UTF8 {
		x := t.d8.Feed(p)
		return ev{r: x.R, n: x.Size, ok: x.Valid, retry: x.Again, wait: x.EOF && x.Size == 0}
	}
	x := t.d16.Feed(p)
	return ev{r: x.R, n: x.Size, ok: x.Valid, retry: x.Again, wait: x.High || x.EOF && x.Size == 0, bom: x.BOM, trunc: x.OddEOF}
}
func (t *Transcoder) finish() ev {
	if t.c.From == UTF8 {
		x := t.d8.Close()
		return ev{n: x.Size, trunc: x.Size > 0}
	}
	x := t.d16.Close()
	return ev{n: x.Size, trunc: x.Size > 0}
}
func (t *Transcoder) emit(x ev, pref bool) error {
	if x.bom {
		if !pref {
			t.s.BOMBytes += int64(x.n)
		}
		if t.c.EmitBOM && !pref {
			return t.put(scalar.ByteOrderMark)
		}
		return nil
	}
	if x.trunc || !x.ok {
		if !pref {
			t.s.Invalid++
			t.s.InvalidBytes += int64(x.n)
		}
		if t.c.Mode == Strict {
			if x.trunc {
				return ErrTruncated
			}
			return ErrInvalid
		}
		if pref {
			return nil
		}
		return t.put(scalar.Replacement)
	}
	if !pref {
		t.s.Scalars++
	}
	if pref {
		return nil
	}
	return t.put(x.r)
}
func (t *Transcoder) put(r scalar.Value) error {
	if t.c.To == UTF8 {
		t.b = append(t.b, u8.Encode(r)...)
		return nil
	}
	e := u16.Little
	if t.c.To == UTF16BE {
		e = u16.Big
	}
	t.b = append(t.b, u16.Encode(r, e)...)
	return nil
}
