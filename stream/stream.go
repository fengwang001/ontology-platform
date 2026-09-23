package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Encoding int

const (
	UTF8 Encoding = iota
	UTF16
)

type Config struct {
	In, Out  Encoding
	Order    u16.Order
	Strict   bool
	EmitBOM  bool
	MaxBytes int
	Offset   int64
}

type Stats struct {
	Scalars  int64
	Invalid  int64
	Bytes    int64
	BOMBytes int64
	Checks   int64
}

type Transcoder struct {
	cfg     Config
	out     []byte
	d8      u8.Decoder
	d16     u16.Decoder
	pending []byte
	stats   Stats
	off     int64
	err     error
	closed  bool
	begin   bool
}

var (
	ErrClosed    = errors.New("stream closed")
	ErrTruncated = errors.New("input truncated")
	ErrLimit     = errors.New("output limit exceeded")
)

type InvalidError struct {
	Offset int64
	Length int
}
type TruncatedError struct {
	Offset int64
	Length int
}

func (e *InvalidError) Error() string   { return "invalid encoding unit" }
func (e *TruncatedError) Error() string { return "input truncated" }
func IsInvalid(err error) bool          { var e *InvalidError; return errors.As(err, &e) }
func IsTruncated(err error) bool        { var e *TruncatedError; return errors.As(err, &e) }

func New(c Config) *Transcoder {
	if c.In == UTF16 {
		return &Transcoder{cfg: c, d16: u16.NewDecoder(c.Order, c.Offset == 0, c.Offset), begin: c.Offset == 0}
	}
	return &Transcoder{cfg: c, d8: u8.NewDecoder(), begin: c.Offset == 0}
}

func (t *Transcoder) Write(p []byte) (int, error) {
	if t.err != nil {
		return 0, t.err
	}
	if t.closed {
		t.err = ErrClosed
		return 0, t.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	held := len(t.pending)
	data := append(append([]byte(nil), t.pending...), p...)
	last := held
	for idx := 0; idx < len(data); {
		abs := t.cfg.Offset + t.off
		var ev, second u8.Event
		bom := false
		if t.cfg.In == UTF8 {
			ev, second = t.d8.StepAt(data[idx], abs)
		} else {
			var isBOM bool
			e, nxt, isBOM := t.d16.StepAt(data[idx], abs)
			ev, second, bom = u8.Event{R: e.R, OK: e.OK, Len: e.Len, Offset: e.Offset, Checks: e.Checks}, nxt, isBOM
		}
		if t.begin && t.cfg.In == UTF8 && ev.OK && ev.R == 0xFEFF && ev.Len == 3 {
			bom = true
		}
		t.begin = false
		t.stats.Checks += int64(ev.Checks)
		if ev.Len == 0 {
			idx, t.off, last = idx+1, t.off+1, idx+1
			continue
		}
		next := idx + ev.Len
		if second.Len != 0 && second.Offset == ev.Offset+int64(ev.Len) {
			next += second.Len
		}
		if err := t.emit(ev, bom); err != nil {
			t.err, t.pending = err, data[last:]
			return max(0, last-held), err
		}
		if second.Len != 0 && second.Offset == ev.Offset+int64(ev.Len) {
			if err := t.emit(second, false); err != nil {
				t.err, t.pending = err, data[next:]
				return max(0, next-held), err
			}
		}
		idx, t.off, last = next, t.off+int64(next-idx), next
	}
	t.pending = data[last:]
	return len(p), nil
}

func (t *Transcoder) emit(ev u8.Event, bom bool) error {
	if bom {
		t.stats.BOMBytes += int64(ev.Len)
		if !t.cfg.EmitBOM {
			return nil
		}
		ev.R = 0xFEFF
	}
	if !ev.OK {
		if t.cfg.Strict {
			return &InvalidError{Offset: ev.Offset, Length: ev.Len}
		}
		t.stats.Invalid++
		t.stats.Bytes += int64(ev.Len)
		ev.R = scalar.Replacement
	} else {
		t.stats.Scalars++
	}
	b := t.encode(ev.R)
	if t.cfg.MaxBytes > 0 && len(t.out)+len(b) > t.cfg.MaxBytes {
		return ErrLimit
	}
	t.out = append(t.out, b...)
	return nil
}

func (t *Transcoder) encode(r scalar.Value) []byte {
	if t.cfg.Out == UTF8 {
		return u8.Encode(r)
	}
	order := t.cfg.Order
	if t.cfg.In == UTF16 {
		order = t.d16.Order()
	}
	return u16.Encode(r, order)
}

func (t *Transcoder) Close() error {
	if t.err != nil {
		return t.err
	}
	if t.closed {
		t.err = ErrClosed
		return t.err
	}
	t.closed = true
	var ev u8.Event
	if t.cfg.In == UTF8 {
		var ok bool
		ev, ok = t.d8.Close()
		if !ok {
			return nil
		}
	} else {
		e, ok, _ := t.d16.Close()
		if !ok {
			return nil
		}
		ev = u8.Event{R: e.R, Len: e.Len, Offset: e.Offset}
	}
	if t.cfg.Strict {
		t.err = &TruncatedError{Offset: ev.Offset, Length: ev.Len}
		return t.err
	}
	if err := t.emit(ev, false); err != nil {
		t.err = err
		return err
	}
	t.off += int64(ev.Len)
	t.pending = nil
	return nil
}

func (t *Transcoder) Output() []byte  { return append([]byte(nil), t.out...) }
func (t *Transcoder) Stats() Stats    { return t.stats }
func (t *Transcoder) Consumed() int64 { return t.cfg.Offset + t.off }
