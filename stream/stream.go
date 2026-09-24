package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

var (
	ErrInvalidByte = errors.New("invalid encoded byte")
	ErrTruncated   = errors.New("encoded input is truncated")
	ErrOutputLimit = errors.New("output limit exceeded")
	ErrClosed      = errors.New("transcoder is closed")
)

const MaxCache = 3

type UnitError struct {
	Err    error
	Offset int64
	Length int
}

func (e *UnitError) Error() string { return e.Err.Error() }
func (e *UnitError) Unwrap() error { return e.Err }

type Config struct {
	FromUTF16 bool
	ToUTF16   bool
	Order     u16.Endian
	Strict    bool
	EmitBOM   bool
	Limit     int64
}

type Stats struct {
	Input        int64
	Valid        int64
	Invalid      int64
	InvalidBytes int64
	BOMBytes     int64
}

type Transcoder struct {
	cfg    Config
	base   int64
	atZero bool
	out    []byte
	closed bool
	fatal  error
	u8     u8.Decoder
	u16    *u16.Decoder
	stats  Stats
	checks int64
}

func New(cfg Config) *Transcoder { return NewAt(cfg, 0) }

func NewAt(cfg Config, base int64) *Transcoder {
	t := &Transcoder{cfg: cfg, base: base, atZero: base == 0, stats: Stats{Input: base}}
	if cfg.FromUTF16 {
		order := cfg.Order
		if order == 0 {
			order = u16.Little
		}
		t.u16 = u16.NewDecoder(order)
	}
	return t
}

func (t *Transcoder) Write(p []byte) (int, error) {
	if t.fatal != nil {
		return 0, t.fatal
	}
	if t.closed {
		return 0, ErrClosed
	}
	n := 0
	for i := 0; i < len(p); i++ {
		if t.Pending() == 0 && t.needReserve(p[i]) && t.cfg.Limit > 0 &&
			int64(len(t.out))+t.maxUnit(p[i]) > t.cfg.Limit {
			err := ErrOutputLimit
			t.fatal = err
			return n, err
		}
		var units []genericUnit
		if t.cfg.FromUTF16 {
			for _, u := range t.u16.Feed(p[i : i+1]) {
				units = append(units, t.fromU16(u))
			}
		} else {
			for _, u := range t.u8.Feed(p[i : i+1]) {
				units = append(units, t.fromU8(u))
			}
		}
		for _, u := range units {
			consumedInP := int(u.end() - t.stats.Input)
			if err := t.emit(u); err != nil {
				t.fatal = err
				t.checks = t.checkCount()
				t.stats.Input = t.consumed()
				if consumedInP > i+1 {
					consumedInP = i + 1
				}
				return consumedInP, err
			}
			if consumedInP > n {
				n = consumedInP
			}
		}
	}
	t.checks = t.checkCount()
	t.stats.Input = t.consumed()
	return len(p), nil
}

func (t *Transcoder) needReserve(b byte) bool {
	if t.cfg.FromUTF16 {
		if t.atZero && t.stats.Input == t.base &&
			((t.cfg.Order != u16.Big && b == 0xFF) || (t.cfg.Order == u16.Big && b == 0xFE)) {
			return false
		}
		return (t.stats.Input-t.base)%2 == 0
	}
	if t.atZero && t.stats.Input == t.base && b == 0xEF {
		return false
	}
	return b >= 0x80
}

func (t *Transcoder) maxUnit(b byte) int64 {
	if t.cfg.ToUTF16 {
		if t.cfg.FromUTF16 || b >= 0xF0 {
			return 4
		}
		return 2
	}
	switch {
	case b < 0x80:
		return 1
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}

func (t *Transcoder) Close() error {
	if t.fatal != nil {
		return t.fatal
	}
	if t.closed {
		return ErrClosed
	}
	t.closed = true
	var u genericUnit
	var ok bool
	if t.cfg.FromUTF16 {
		x, yes := t.u16.Flush()
		u, ok = t.fromU16(x), yes
	} else {
		x, yes := t.u8.Flush()
		u, ok = t.fromU8(x), yes
	}
	if ok {
		if err := t.emit(u); err != nil {
			t.fatal = err
		}
	}
	t.checks = t.checkCount()
	t.stats.Input = t.consumed()
	err := t.fatal
	t.fatal = nil
	return err
}

func (t *Transcoder) Output() []byte { return append([]byte(nil), t.out...) }
func (t *Transcoder) Stats() Stats   { return t.stats }
func (t *Transcoder) Checks() int64  { return t.checks }
func (t *Transcoder) Pending() int {
	if t.cfg.FromUTF16 {
		return t.u16.Pending()
	}
	return t.u8.Pending()
}

type genericUnit struct {
	kind  byte
	r     scalar.Rune
	start int64
	size  int
}

const (
	uOK = iota
	uInvalid
	uTrunc
	uBOM
)

func (t *Transcoder) fromU8(u u8.Unit) genericUnit {
	k := byte(uOK)
	if u.Kind == u8.Invalid {
		k = uInvalid
	}
	if u.Kind == u8.Truncated {
		k = uTrunc
	}
	if u.R == scalar.BOM && u.Start == 0 && t.atZero {
		k = uBOM
	}
	return genericUnit{k, u.R, u.Start, u.Size}
}

func (t *Transcoder) fromU16(u u16.Unit) genericUnit {
	k := byte(uOK)
	if u.Kind == u16.Invalid {
		k = uInvalid
	}
	if u.Kind == u16.Truncated {
		k = uTrunc
	}
	if u.Kind == u16.BOMKind && t.atZero {
		k = uBOM
	} else if u.Kind == u16.BOMKind {
		k = uOK
	}
	return genericUnit{k, u.R, u.Start, u.Size}
}

func (u genericUnit) end() int64 { return u.start + int64(u.size) }

func (t *Transcoder) emit(u genericUnit) error {
	switch u.kind {
	case uInvalid:
		t.stats.Invalid++
		t.stats.InvalidBytes += int64(u.size)
		if t.cfg.Strict {
			return &UnitError{ErrInvalidByte, t.base + u.start, u.size}
		}
		return t.writeScalar(scalar.Replacement)
	case uTrunc:
		if t.cfg.Strict {
			return &UnitError{ErrTruncated, t.base + u.start, u.size}
		}
		t.stats.Invalid++
		t.stats.InvalidBytes += int64(u.size)
		return t.writeScalar(scalar.Replacement)
	case uBOM:
		t.stats.BOMBytes += int64(u.size)
		if !t.cfg.EmitBOM {
			return nil
		}
		return t.writeScalar(scalar.BOM)
	default:
		t.stats.Valid++
		return t.writeScalar(u.r)
	}
}

func (t *Transcoder) writeScalar(r scalar.Rune) error {
	need := int64(4)
	if r <= 0x7F {
		need = 1
	} else if r <= 0x7FF {
		need = 2
	} else if r <= 0xFFFF {
		need = 3
	}
	if t.cfg.ToUTF16 && r <= 0xFFFF {
		need = 2
	}
	if t.cfg.Limit > 0 && int64(len(t.out))+need > t.cfg.Limit {
		return ErrOutputLimit
	}
	if t.cfg.ToUTF16 {
		t.out = u16.Encode(t.out, r, t.targetOrder())
	} else {
		t.out = u8.Encode(t.out, r)
	}
	return nil
}

func (t *Transcoder) targetOrder() u16.Endian {
	order := t.cfg.Order
	if t.cfg.FromUTF16 {
		order = t.u16.Order()
	}
	if order == u16.Big {
		return u16.Big
	}
	return u16.Little
}

func (t *Transcoder) consumed() int64 {
	if t.cfg.FromUTF16 {
		return t.base + t.u16.Checks()
	}
	return t.base + t.u8.Checks()
}

func (t *Transcoder) checkCount() int64 {
	if t.cfg.FromUTF16 {
		return t.u16.Checks()
	}
	return t.u8.Checks()
}
