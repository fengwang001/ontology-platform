package stream

import (
	"errors"
	"fmt"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Encoding uint8

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

type Config struct {
	From    Encoding
	To      Encoding
	Strict  bool
	EmitBOM bool
	Limit   int
	NoBOM   bool
}

type Stats struct {
	Scalars      int64
	Invalid      int64
	InvalidBytes int64
	BOMBytes     int64
	Consumed     int64
	Output       int64
	Checks       int64
	AlignChecks  int64
}

const MaxBuffer = 3

var (
	ErrInvalid     = errors.New("invalid unicode unit")
	ErrTruncated   = errors.New("truncated unicode input")
	ErrOutputLimit = errors.New("output limit exceeded")
	ErrClosed      = errors.New("transcoder closed")
)

type UnitError struct {
	Cause  error
	Offset int64
	Length int
}

func (e *UnitError) Error() string {
	return fmt.Sprintf("%v at byte %d length %d", e.Cause, e.Offset, e.Length)
}

func (e *UnitError) Unwrap() error { return e.Cause }

type Transcoder struct {
	cfg        Config
	order u16.Order
	out        []byte
	pending    []byte
	stats      Stats
	terminal error
	closed bool
	leading bool
	stage int
	high uint16
	start int64
	base  int64
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, leading: !cfg.NoBOM}
	if cfg.From == UTF16BE {
		t.order = u16.BE
	}
	return t
}

func NewAt(cfg Config, offset int64) *Transcoder {
	t := New(cfg)
	t.base = offset
	return t
}

func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal != nil {
		return 0, t.terminal
	}
	if t.closed {
		return 0, ErrClosed
	}
	before := t.stats.Consumed
	for i := 0; i < len(p); i++ {
		t.stats.Checks++
		if t.cfg.From == UTF8 {
			i = t.byteUTF8(p, i)
		} else {
			i = t.byteUTF16(p, i)
		}
		if t.terminal != nil {
			break
		}
	}
	return int(t.stats.Consumed - before), t.terminal
}

func (t *Transcoder) Close() error {
	if t.terminal != nil {
		return t.terminal
	}
	if t.closed {
		return ErrClosed
	}
	t.closed = true
	if len(t.pending) == 0 {
		return nil
	}
	return t.badTail()
}

func (t *Transcoder) Output() []byte { return t.out }

func (t *Transcoder) Stats() Stats {
	s := t.stats
	s.Output = int64(len(t.out))
	return s
}

func (t *Transcoder) Pending() []byte {
	return append([]byte(nil), t.pending...)
}

func (t *Transcoder) byteUTF8(p []byte, i int) int {
	b := p[i]
	if len(t.pending) == 0 {
		t.start = t.base + t.stats.Consumed + int64(i)
		k := scalar.LeadKind(b)
		if k == scalar.Ascii {
			t.finishScalar(rune(b), 1)
			return i
		}
		if scalar.Need(k) == 0 {
			t.finishInvalid(t.start, 1)
			return i
		}
		t.stage = scalar.Need(k)
		t.pending = append(t.pending[:0], b)
		return i
	}
	lead := t.pending[0]
	ok := scalar.Cont(b) && (len(t.pending) != 1 || scalar.SecondOK(lead, b))
	if ok {
		t.pending = append(t.pending, b)
		if len(t.pending) == t.stage {
			t.completeUTF8()
		}
		return i
	}
	n := len(t.pending)
	t.finishInvalid(t.start, n)
	t.pending = t.pending[:0]
	t.stage = 0
	return i - 1
}

func (t *Transcoder) completeUTF8() {
	p := t.pending
	var r rune
	switch len(p) {
	case 2:
		r = rune(p[0]&0x1f)<<6 | rune(p[1]&0x3f)
	case 3:
		r = rune(p[0]&0x0f)<<12 | rune(p[1]&0x3f)<<6 | rune(p[2]&0x3f)
	case 4:
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3f)<<12 | rune(p[2]&0x3f)<<6 | rune(p[3]&0x3f)
	}
	if !scalar.IsScalar(r) {
		t.finishInvalid(t.start, len(p))
	} else {
		t.finishScalar(r, len(p))
	}
}

func (t *Transcoder) byteUTF16(p []byte, i int) int {
	if len(t.pending) == 0 {
		t.start = t.base + t.stats.Consumed + int64(i)
	}
	t.pending = append(t.pending, p[i])
	if len(t.pending)%2 == 1 || (t.high != 0 && len(t.pending) == 3) {
		return i
	}
	if t.high != 0 {
		return t.completeUTF16Pair(i)
	}
	v := u16Value(t.pending, t.order)
	t.pending = t.pending[:0]
	t.acceptUTF16(v, t.start)
	return i
}

func (t *Transcoder) completeUTF16Pair(i int) int {
	low := u16Value(t.pending[2:], t.order)
	if scalar.IsLowSurrogate(low) {
		r := rune(t.high-0xd800)<<10 + rune(low-0xdc00) + 0x10000
		t.high = 0
		t.pending = t.pending[:0]
		t.finishScalar(r, 4)
		return i
	}
	t.finishInvalid(t.start, 2)
	t.high = 0
	v := u16Value(t.pending[2:], t.order)
	t.pending = t.pending[:0]
	t.acceptUTF16(v, t.base+t.stats.Consumed+int64(i)-1)
	return i
}

func (t *Transcoder) acceptUTF16(v uint16, offset int64) {
	switch {
	case scalar.IsHighSurrogate(v):
		t.high = v
		t.start = offset
		t.pending = append(t.pending[:0], 0, 0)
		u16.Encode(rune(v), t.pending, t.order)
	case scalar.IsLowSurrogate(v):
		t.finishInvalid(offset, 2)
	default:
		t.finishScalar(rune(v), 2)
	}
}

func (t *Transcoder) finishScalar(r rune, n int) {
	if t.leading {
		t.leading = false
		if r == 0xfeff {
			size := 3
			if t.cfg.From != UTF8 {
				size = 2
			}
			t.stats.BOMBytes += int64(size)
			t.stats.Consumed += int64(n)
			if t.cfg.EmitBOM {
				t.emit(0xfeff)
			}
			t.pending = t.pending[:0]
			return
		}
	}
	if !t.emit(r) {
		return
	}
	t.stats.Scalars++
	t.stats.Consumed += int64(n)
	t.pending = t.pending[:0]
}

func (t *Transcoder) finishInvalid(offset int64, n int) {
	if t.cfg.Strict {
		t.terminal = &UnitError{Cause: ErrInvalid, Offset: offset, Length: n}
		return
	}
	t.stats.Invalid++
	t.stats.InvalidBytes += int64(n)
	t.stats.Consumed += int64(n)
	t.leading = false
	t.pending = t.pending[:0]
	t.emit(0xfffd)
}

func (t *Transcoder) badTail() error {
	n := len(t.pending)
	if t.high != 0 {
		n = 2
	}
	if t.cfg.Strict {
		t.terminal = &UnitError{Cause: ErrTruncated, Offset: t.start, Length: n}
		return t.terminal
	}
	t.stats.Invalid++
	t.stats.InvalidBytes += int64(len(t.pending))
	t.stats.Consumed += int64(len(t.pending))
	t.emit(0xfffd)
	t.pending = t.pending[:0]
	return nil
}

func (t *Transcoder) emit(r rune) bool {
	n := u8.EncodedLen(r)
	if t.cfg.To != UTF8 {
		n = u16.EncodedLen(r)
	}
	if t.cfg.Limit > 0 && len(t.out)+n > t.cfg.Limit {
		t.terminal = ErrOutputLimit
		return false
	}
	old := len(t.out)
	t.out = append(t.out, make([]byte, n)...)
	if t.cfg.To == UTF8 {
		u8.Encode(r, t.out[old:])
	} else {
		order := u16.LE
		if t.cfg.To == UTF16BE {
			order = u16.BE
		}
		u16.Encode(r, t.out[old:], order)
	}
	return true
}

func u16Value(p []byte, order u16.Order) uint16 {
	if order == u16.BE {
		return uint16(p[0])<<8 | uint16(p[1])
	}
	return uint16(p[1])<<8 | uint16(p[0])
}
