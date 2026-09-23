package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Format uint8

const (
	UTF8 Format = iota + 1
	UTF16
	UTF16LE
	UTF16BE
)

type Config struct {
	From       Format
	To         Format
	Strict     bool
	Limit      int
	EmitBOM    bool
	GlobalBase int
	OwnStart   int
	OwnEnd     int
}

type Stats struct {
	Consumed     int
	Scalars      int
	Invalid      int
	InvalidBytes int
	BOMBytes     int
	Checked      int
}

var (
	ErrInvalid   = errors.New("invalid encoded unit")
	ErrTruncated = errors.New("truncated input")
	ErrLimit     = errors.New("output limit exceeded")
	ErrClosed    = errors.New("transcoder is in terminal state")
)

type Transcoder struct {
	cfg      Config
	u8d      *u8.Decoder
	u16d     *u16.Decoder
	fromUTF8 bool
	toUTF8   bool
	order    u16.Endian
	detect   bool
	out      []byte
	pend     []byte
	stats    Stats
	owned    Stats
	fatal    error
	closed   bool
	bomDone  bool
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	t.fromUTF8 = cfg.From == UTF8
	t.toUTF8 = cfg.To == UTF8
	if !t.fromUTF8 {
		t.order, t.detect = u16.Little, cfg.From == UTF16
		if cfg.From == UTF16BE {
			t.order, t.detect = u16.Big, false
		}
		t.u16d = u16.NewDecoder(t.order)
	} else {
		t.u8d = u8.NewDecoder()
	}
	if cfg.To == UTF16BE {
		t.order = u16.Big
	}
	if cfg.To == UTF16LE || cfg.To == UTF16 {
		t.order = u16.Little
	}
	return t
}

type event struct {
	kind u8.Kind
	r    uint32
	n    int
	off  int
}

func decode(d *u8.Decoder, b byte) ([]event, byte, bool) {
	es, r, again := d.Step(b)
	out := make([]event, len(es))
	for i, e := range es {
		out[i] = event{e.Kind, e.R, e.Len, 0}
	}
	return out, r, again
}

func decode16(d *u16.Decoder, b byte) ([]event, byte, bool) {
	es, r, again := d.Step(b)
	out := make([]event, len(es))
	for i, e := range es {
		out[i] = event{u8.Kind(e.Kind), e.R, e.Len, 0}
	}
	return out, r, again
}

func close8(d *u8.Decoder) (event, bool) {
	e, ok := d.Close()
	return event{e.Kind, e.R, e.Len, 0}, ok
}

func close16(d *u16.Decoder) (event, bool) {
	e, ok := d.Close()
	return event{u8.Kind(e.Kind), e.R, e.Len, 0}, ok
}

func (t *Transcoder) isOwned(off int) bool {
	end := t.cfg.OwnEnd == 0 || off < t.cfg.OwnEnd
	return off >= t.cfg.OwnStart && end
}

func (t *Transcoder) putR(r uint32) bool {
	var ok bool
	if t.toUTF8 {
		n := u8.EncodedLen(r)
		if n == 0 || (t.cfg.Limit > 0 && len(t.out)+n > t.cfg.Limit) {
			return false
		}
		t.out, ok = u8.AppendEncode(t.out, r)
	} else {
		n := u16.EncodedLen(r)
		if n == 0 || (t.cfg.Limit > 0 && len(t.out)+n > t.cfg.Limit) {
			return false
		}
		t.out, ok = u16.AppendEncode(t.out, r, t.order)
	}
	return ok
}

func (t *Transcoder) bom(off int) error {
	if t.bomDone || off != 0 {
		return nil
	}
	t.bomDone = true
	if !t.cfg.EmitBOM || !t.putR(scalar.ByteOrderMark) {
		return ErrLimit
	}
	return nil
}

func (t *Transcoder) emit(e event) error {
	if !t.ownedStats(e.off) {
		return nil
	}
	if e.kind == u8.Invalid {
		if t.cfg.Strict {
			return &InvalidError{Offset: e.off, Len: e.n}
		}
		e.r = scalar.Replacement
	}
	if e.r == scalar.ByteOrderMark && e.off == 0 {
		if t.bom(0) != nil {
			return ErrLimit
		}
	} else if !t.putR(e.r) {
		return ErrLimit
	}
	return nil
}

func (t *Transcoder) count(e event) {
	t.stats.Consumed += e.n
	if !t.ownedStats(e.off) {
		return
	}
	t.ownedStats.Consumed += e.n
	if e.kind == u8.Invalid {
		t.ownedStats.Invalid++
		t.ownedStats.InvalidBytes += e.n
	} else {
		t.ownedStats.Scalars++
	}
	if e.r == scalar.ByteOrderMark && e.off == 0 {
		t.ownedStats.BOMBytes += e.n
	}
}

func (t *Transcoder) feed(b byte) error {
	var es []event
	var replay byte
	var again bool
	if t.fromUTF8 {
		es, replay, again = decode(t.u8d, b)
	} else {
		es, replay, again = decode16(t.u16d, b)
	}
	for {
		off := t.cfg.GlobalBase + t.stats.Consumed
		for _, e := range es {
			ev := event{e.Kind, e.R, e.Len, off}
			if t.detect && e.kind == u8.Scalar && (e.R == 0xFFFE || e.R == 0xFEFF) && off == 0 {
				t.order = u16.Big
				t.u16d.SetOrder(t.order)
				ev.r = scalar.ByteOrderMark
			}
			if err := t.emit(ev); err != nil {
				if err != ErrLimit {
					t.count(ev)
				}
				return err
			}
			t.count(ev)
			off += e.Len
		}
		if !again {
			return nil
		}
		again = false
		b = replay
		if t.fromUTF8 {
			es, replay, again = decode(t.u8d, b)
		} else {
			es, replay, again = decode16(t.u16d, b)
		}
	}
}

func (t *Transcoder) Write(p []byte) (int, error) {
	if t.fatal != nil || t.closed {
		return 0, t.fatal
	}
	baseConsumed, start := t.stats.Consumed, len(t.pend)
	for _, b := range p {
		t.pend = append(t.pend, b)
		if err := t.feed(b); err != nil {
			if err == ErrLimit {
				t.fatal = err
			} else {
				t.fatal = err
			}
			break
		}
	}
	pl := 0
	if t.fromUTF8 {
		pl = t.u8d.PendingLen()
	} else {
		pl = t.u16d.PendingLen()
	}
	if len(t.pend) > pl {
		t.pend = t.pend[len(t.pend)-pl:]
	}
	n := t.stats.Consumed - baseConsumed - start + pl
	if n < 0 || t.fatal == ErrLimit {
		n = t.stats.Consumed - baseConsumed - start
	}
	return n, t.fatal
}

func (t *Transcoder) Close() error {
	if t.fatal != nil || t.closed {
		return t.fatal
	}
	t.closed = true
	if err := t.bom(t.cfg.GlobalBase + t.stats.Consumed); err != nil {
		t.fatal = err
		return err
	}
	var e event
	var ok bool
	if t.fromUTF8 {
		e, ok = close8(t.u8d)
	} else {
		e, ok = close16(t.u16d)
	}
	if !ok {
		return nil
	}
	off := t.cfg.GlobalBase + t.stats.Consumed
	err := t.emit(event{u8.Invalid, scalar.Replacement, e.n, off})
	if err != nil {
		if t.cfg.Strict {
			err = &TruncatedError{Offset: off, Len: e.Len}
		}
		t.fatal = err
		return err
	}
	t.count(event{u8.Invalid, scalar.Replacement, e.n, off})
	return nil
}

func (t *Transcoder) Output() []byte { return t.out }

func (t *Transcoder) Stats() Stats {
	if t.cfg.OwnStart == 0 && t.cfg.OwnEnd == 0 {
		t.stats.Checked = t.checks()
		return t.stats
	}
	t.ownedStats.Checked = t.checks()
	return t.ownedStats
}

func (t *Transcoder) checks() int {
	if t.fromUTF8 {
		return t.u8d.Checked
	}
	return t.u16d.Checked
}

func (t *Transcoder) OwnedStats() Stats {
	t.ownedStats.Checked = t.checks()
	return t.ownedStats
}

func (t *Transcoder) Pending() []byte { return t.pend }
