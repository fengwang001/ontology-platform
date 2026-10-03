// Package stream: streaming UTF-8 <-> UTF-16 transcoder with invalid-unit replacement. Not safe for concurrent use.
package stream

import (
	"errors"
	"fmt"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

var ErrInvalid = errors.New("stream: invalid input unit")
var ErrTruncated = errors.New("stream: truncated input")
var ErrLimit = errors.New("stream: output limit exceeded")
var ErrClosed = errors.New("stream: write after terminal state")

type Error struct {
	Kind error
	Off  int64
	Len  int
}

func (e *Error) Error() string { return fmt.Sprintf("%v at offset %d, len %d", e.Kind, e.Off, e.Len) }
func (e *Error) Unwrap() error { return e.Kind }

type Config struct {
	InUTF16, OutUTF16, LE    bool  // LE: UTF-16 byte order (input default & output)
	Strict, KeepBOM, SkipBOM bool  // SkipBOM: no BOM recognition (par segments)
	MaxOut                   int   // output byte limit, 0 = unlimited
	BaseOff                  int64 // absolute offset of first input byte
}
type Stats struct {
	Scalars, GoodBytes, Invalid, BadBytes, BOMBytes, Consumed int64
}
type Transcoder struct {
	cfg       Config
	out, pend []byte // pend: split buffer, residual cap 3 (DESIGN.md)
	stats     Stats
	bomSeen   bool
	err       error
	checks    int64 // input bytes examined by the state machine
}

func New(cfg Config) *Transcoder     { return &Transcoder{cfg: cfg} }
func (t *Transcoder) Output() []byte { return t.out }
func (t *Transcoder) Stats() Stats   { return t.stats }
func (t *Transcoder) Checks() int64  { return t.checks }
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.err != nil {
		return 0, t.err
	}
	start := t.stats.Consumed
	for i := 0; i < len(p) && t.err == nil; i++ {
		t.checks++
		t.stats.Consumed++
		t.step(p[i])
	}
	if t.err != nil {
		return max(int(t.stats.Consumed-start)-len(t.pend), 0), t.err
	}
	return len(p), nil
}
func (t *Transcoder) Close() error {
	if t.err != nil {
		return t.err
	}
	if len(t.pend) > 0 {
		if t.cfg.Strict {
			t.err = &Error{Kind: ErrTruncated, Off: t.cfg.BaseOff + t.stats.Consumed - int64(len(t.pend)), Len: len(t.pend)}
			t.stats.Invalid++
			t.stats.BadBytes += int64(len(t.pend))
			t.pend = nil
			return t.err
		}
		t.flushBad(t.cfg.BaseOff + t.stats.Consumed - int64(len(t.pend)))
	}
	err := t.err
	if err == nil {
		t.err = ErrClosed
	}
	return err
}
func (t *Transcoder) step(b byte) {
	t.pend = append(t.pend, b)
	r, n, st := u8.Decode(t.pend)
	if t.cfg.InUTF16 {
		r, n, st = u16.Decode(t.pend, u16.Order(t.cfg.LE))
	}
	switch st {
	case scalar.Short:
	case scalar.OK:
		t.flushGood(r)
	default:
		off := t.cfg.BaseOff + t.stats.Consumed - int64(len(t.pend))
		rest := append([]byte(nil), t.pend[n:]...)
		t.pend = t.pend[:n]
		t.flushBad(off)
		i := 0
		for i < len(rest) && t.err == nil {
			t.step(rest[i]) // reclassify within the same examination
			i++
		}
		t.pend = append(t.pend, rest[i:]...)
	}
}
func (t *Transcoder) flushGood(r int32) {
	if !t.bomSeen {
		t.bomSeen = true
		if !t.cfg.SkipBOM && (r == 0xFEFF || t.cfg.InUTF16 && r == 0xFFFE) {
			if r == 0xFFFE {
				t.cfg.LE = !t.cfg.LE
			}
			t.stats.BOMBytes += int64(len(t.pend))
			if t.cfg.KeepBOM {
				t.emit(0xFEFF)
			}
			t.pend = t.pend[:0]
			return
		}
	}
	if t.emit(r) {
		t.stats.Scalars++
		t.stats.GoodBytes += int64(len(t.pend))
		t.pend = t.pend[:0]
	}
}
func (t *Transcoder) flushBad(off int64) {
	t.bomSeen = true
	if t.cfg.Strict {
		t.err = &Error{Kind: ErrInvalid, Off: off, Len: len(t.pend)}
	} else if !t.emit(scalar.Replacement) {
		return
	}
	t.stats.Invalid++
	t.stats.BadBytes += int64(len(t.pend))
	t.pend = t.pend[:0]
}
func (t *Transcoder) emit(r int32) bool {
	mark := len(t.out)
	if t.cfg.OutUTF16 {
		t.out = u16.Append(t.out, r, u16.Order(t.cfg.LE))
	} else {
		t.out = u8.Append(t.out, r)
	}
	if t.cfg.MaxOut > 0 && len(t.out) > t.cfg.MaxOut {
		t.out, t.err = t.out[:mark], ErrLimit
		return false
	}
	return true
}
