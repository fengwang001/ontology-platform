package stream

import (
	"errors"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Direction int

const (
	UTF8ToUTF16LE Direction = iota
	UTF8ToUTF16BE
	UTF16LEToUTF8
	UTF16BEToUTF8
)

var (
	ErrIllegal   = errors.New("illegal encoding unit")
	ErrTruncated = errors.New("truncated input")
	ErrLimit     = errors.New("output limit exceeded")
	ErrClosed    = errors.New("transcoder is in terminal state")
)

type Error struct{ Kind error; Offset int64; Length int }
func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type Config struct {
	Direction Direction
	Strict bool
	KeepBOM bool
	NoInitialBOM bool
	OutputLimit int
}

type Stats struct {
	ConsumedBytes, Scalars, IllegalUnits, IllegalBytes, BOMBytes, BytesChecked int64
	MaxPending int
}

type Transcoder struct {
	cfg Config
	out, pend []byte
	terminal error
	need int
	min2, max2 byte
	high rune
	bomDone bool
	odd byte
	hasOdd, wantLow bool
	stats Stats
}

func New(c Config) *Transcoder { return &Transcoder{cfg: c, pend: make([]byte, 0, 3)} }

func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal != nil { return 0, t.terminal }
	before := t.stats.ConsumedBytes
	if q, wait, ok := t.initialBOM(p); ok {
		t.stats.BytesChecked += int64(len(q)); return len(q), nil
	} else if wait {
		t.stats.BytesChecked += int64(len(q)); return 0, nil
	} else {
		p = q; t.stats.BytesChecked += int64(len(q))
	}
	var err error
	if t.cfg.Direction <= UTF8ToUTF16BE { err = t.write8(p) } else { err = t.write16(p) }
	return int(t.stats.ConsumedBytes - before), err
}

func (t *Transcoder) Close() error {
	if t.terminal != nil { return t.terminal }
	if len(t.pend) == 0 && !t.hasOdd && !t.wantLow { return nil }
	n := len(t.pend); if t.hasOdd { n = 1 }; if t.wantLow { n = 2 }
	err := t.fail(ErrTruncated, n)
	if t.cfg.Strict { t.terminal = err; return err }
	t.stats.ConsumedBytes += int64(n); t.stats.IllegalUnits++
	if t.cfg.Direction <= UTF8ToUTF16BE { t.stats.IllegalBytes += int64(n) }
	t.emit(scalar.Replacement)
	t.pend, t.hasOdd, t.wantLow = nil, false, false
	return nil
}

func (t *Transcoder) Output() []byte { return append([]byte(nil), t.out...) }
func (t *Transcoder) Stats() Stats { return t.stats }
func (t *Transcoder) PendingLen() int { return len(t.pend)+b2i(t.hasOdd)+2*b2i(t.wantLow) }
func (t *Transcoder) Resume() []byte { return append([]byte(nil), t.pend...) }

func (t *Transcoder) write8(p []byte) error {
	for i := 0; i < len(p); i++ {
		b := p[i]
		t.stats.BytesChecked++
		if t.need == 0 {
			if b < 0x80 { if err := t.scalar(rune(b), 1); err != nil { return err }; continue }
			t.need, t.min2, t.max2 = u8.LeadSpec(b); t.pend = append(t.pend, b)
			if t.need == 0 { if err := t.bad(1); err != nil { return err } }
			continue
		}
		pos := len(t.pend)
		if pos == 1 && (b < byte(t.min2) || b > byte(t.max2)) {
			if err := t.bad(1); err != nil { return err }
			if b >= 0x80 { t.feedLead(b) } else if err := t.scalar(rune(b), 1); err != nil { return err }
			continue
		}
		if b < 0x80 || b > 0xbf {
			if err := t.bad(pos); err != nil { return err }
			if b >= 0x80 { t.feedLead(b) } else if err := t.scalar(rune(b), 1); err != nil { return err }
			continue
		}
		if len(t.pend) < t.need-1 {
			t.pend = append(t.pend, b); t.notePending(); continue
		}
		t.pend = append(t.pend, b)
		r := decode(t.pend)
		if !scalar.IsValid(r) { if err := t.bad(1); err != nil { return err }; continue }
		if err := t.scalar(r, len(t.pend)); err != nil { return err }
	}
	t.notePending(); return nil
}

func (t *Transcoder) write16(p []byte) error {
	little := t.cfg.Direction == UTF16LEToUTF8
	for i := 0; i < len(p); i++ {
		b := p[i]
		t.stats.BytesChecked++
		if !t.hasOdd { t.odd, t.hasOdd = b, true; continue }
		t.hasOdd = false
		code := rune(t.odd)<<8 | rune(b); if little { code = rune(t.odd) | rune(b)<<8 }
		if t.wantLow {
			if scalar.IsLowSurrogate(code) {
				t.wantLow = false
				if err := t.scalar(scalar.SurrogatePair(t.high, code), 4); err != nil { return err }
				continue
			}
			t.wantLow = false
			if err := t.bad16(2); err != nil { return err }
		}
		if scalar.IsLowSurrogate(code) { if err := t.bad16(2); err != nil { return err }; continue }
		if scalar.IsHighSurrogate(code) { t.high, t.wantLow = code, true; continue }
		if err := t.scalar(code, 2); err != nil { return err }
	}
	return nil
}

func (t *Transcoder) initialBOM(p []byte) ([]byte, bool, bool) {
	var b []byte
	if t.cfg.NoInitialBOM || t.bomDone { return p, false, false }
	t.bomDone = true
	if t.cfg.Direction <= UTF8ToUTF16BE {
		b = []byte{0xef, 0xbb, 0xbf}
	} else {
		b = u16.CodeBytes(0xfeff, t.cfg.Direction == UTF16LEToUTF8)
	}
	all := append(t.pend, p...)
	if n, ok := prefixMatch(b, all); ok {
		t.stats.BOMBytes += int64(len(b))
		if t.cfg.KeepBOM { t.emit(0xfeff) }
		t.pend = nil
		return nil, false, true
	} else if n > 0 {
		if len(all) < len(b) {
			t.pend = append(t.pend, p...); t.notePending()
			return p, true, false
		}
		q := append([]byte(nil), t.pend...); t.pend = nil
		return append(q, p...), false, false
	}
	return p, false, false
}

func prefixMatch(want, got []byte) (int, bool) {
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			if i == 0 { return 0, false }
			return i, false
		}
	}
	if len(got) < len(want) { return len(got), false }
	return len(want), true
}

func (t *Transcoder) scalar(r rune, n int) error {
	if err := t.emit(r); err != nil { return err }
	t.stats.ConsumedBytes += int64(n); t.stats.Scalars++; t.pend, t.need = nil, 0; return nil
}

func (t *Transcoder) bad(n int) error {
	err := t.fail(ErrIllegal, n)
	if t.cfg.Strict { t.terminal = err; return err }
	t.replace(n); return nil
}

func (t *Transcoder) bad16(n int) error {
	err := t.fail(ErrIllegal, n)
	if t.cfg.Strict { t.terminal = err; return err }
	t.stats.ConsumedBytes += int64(n); t.stats.IllegalUnits++; t.emit(scalar.Replacement)
	return nil
}

func (t *Transcoder) replace(n int) {
	t.stats.ConsumedBytes += int64(n); t.stats.IllegalUnits++; t.stats.IllegalBytes += int64(n)
	t.pend = t.pend[n:]; t.need = 0; t.emit(scalar.Replacement); t.notePending()
}

func (t *Transcoder) emit(r rune) error {
	var b []byte
	if t.cfg.Direction <= UTF8ToUTF16BE { b = u16.Encode(r, t.cfg.Direction == UTF8ToUTF16LE) } else { b = u8.Encode(r) }
	if t.cfg.OutputLimit > 0 && len(t.out)+len(b) > t.cfg.OutputLimit {
		t.terminal = &Error{Kind: ErrLimit, Offset: t.stats.ConsumedBytes, Length: len(b)}; return t.terminal
	}
	t.out = append(t.out, b...); return nil
}

func (t *Transcoder) fail(kind error, n int) *Error {
	return &Error{Kind: kind, Offset: t.stats.ConsumedBytes, Length: n}
}

func (t *Transcoder) feedLead(b byte) {
	t.pend, t.need = nil, 0
	t.need, t.min2, t.max2 = u8.LeadSpec(b); t.pend = append(t.pend, b)
	if t.need == 0 { t.bad(1) }
}

func (t *Transcoder) notePending() { if n := len(t.pend); n > t.stats.MaxPending { t.stats.MaxPending = n } }
func b2i(b bool) int { if b { return 1 }; return 0 }

func decode(p []byte) rune {
	r := rune(p[0] & (0xff >> uint(len(p))))
	for _, b := range p[1:] { r = r<<6 | rune(b&0x3f) }
	return r
}
