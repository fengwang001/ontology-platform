// Package stream provides streaming, split-safe UTF-8 <-> UTF-16 transcoding.
// A single Transcoder is not safe for concurrent use.
package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

type Enc int

const (
	UTF8 Enc = iota
	UTF16LE
	UTF16BE
)

// Sentinel, mutually distinguishable errors.
var (
	ErrIllegal   = errors.New("stream: illegal byte sequence")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output size limit exceeded")
	ErrClosed    = errors.New("stream: write after close")
	ErrNoBOM     = errors.New("stream: UTF-16 input missing BOM")
)

// OffsetError carries the global start offset and length of the bad unit.
type OffsetError struct {
	Err    error
	Offset int
	Len    int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Config configures a Transcoder; MaxOut 0 = unlimited.
type Config struct {
	From, To Enc
	Strict   bool
	MaxOut   int
	KeepBOM  bool
	Base     int // global offset of the first input byte
}

// Stats satisfies ValidBytes+BadBytes+BOMBytes == Consumed.
type Stats struct {
	Scalars, BadUnits, BadBytes, BOMBytes, ValidBytes, Consumed, Checks int64
}

type unit struct {
	r                 rune
	bad, trunc, noBOM bool
	n                 int
}

// Transcoder is an io.WriteCloser; not safe for concurrent use.
type Transcoder struct {
	cfg   Config
	d8    *u8.Decoder
	d16   *u16.Decoder
	out   []byte
	st    Stats
	off   int // committed source bytes (held prefix excluded)
	first bool
	term  error
}

// New returns a Transcoder; use NewAuto for BOM-selected UTF-16 input.
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, first: true}
	if cfg.From == UTF8 {
		t.d8 = u8.NewDecoder()
	} else if cfg.From == UTF16BE {
		t.d16 = u16.NewDecoder(u16.BE)
	} else {
		t.d16 = u16.NewDecoder(u16.LE)
	}
	return t
}

// NewAuto selects UTF-16 byte order from the stream-initial BOM.
func NewAuto(cfg Config) *Transcoder {
	t := New(cfg)
	t.d16, t.d8 = u16.NewDecoder(-1), nil
	return t
}

func (t *Transcoder) feed(b byte) []unit {
	if t.d8 != nil {
		us := t.d8.Feed(b)
		out := make([]unit, len(us))
		for i, u := range us {
			out[i] = unit{r: u.R, bad: u.Bad, trunc: u.Trunc, n: u.N}
		}
		return out
	}
	us := t.d16.Feed(b)
	out := make([]unit, len(us))
	for i, u := range us {
		out[i] = unit{r: u.R, bad: u.Bad, trunc: u.Trunc, noBOM: u.NoBOM, n: u.N}
	}
	return out
}

func (t *Transcoder) finish() []unit {
	var us []unit
	if t.d8 != nil {
		for _, u := range t.d8.Finish() {
			us = append(us, unit{r: u.R, bad: u.Bad, trunc: u.Trunc, n: u.N})
		}
	} else {
		for _, u := range t.d16.Finish() {
			us = append(us, unit{r: u.R, bad: u.Bad, trunc: u.Trunc, noBOM: u.NoBOM, n: u.N})
		}
	}
	return us
}

// Output returns a copy of produced output.
func (t *Transcoder) Output() []byte { return append([]byte(nil), t.out...) }

// Stats returns current counters.
func (t *Transcoder) Stats() Stats {
	s := t.st
	if t.d8 != nil {
		s.Checks = t.d8.Checks()
	} else {
		s.Checks = t.d16.Checks()
	}
	s.Consumed = int64(t.off)
	return s
}

// Err returns the terminal error, if any.
func (t *Transcoder) Err() error { return t.term }

func (t *Transcoder) emit(r rune) bool {
	n := u16.EncodeLen(r)
	if t.cfg.To == UTF8 {
		n = len8(r)
	}
	if t.cfg.MaxOut > 0 && len(t.out)+n > t.cfg.MaxOut {
		return false
	}
	if t.cfg.To == UTF8 {
		t.out = u8.Encode(t.out, r)
	} else if t.cfg.To == UTF16BE {
		t.out = u16.Encode(t.out, r, u16.BE)
	} else {
		t.out = u16.Encode(t.out, r, u16.LE)
	}
	return true
}

func len8(r rune) int {
	if r < 0x80 {
		return 1
	}
	if r < 0x800 {
		return 2
	}
	if r < 0x10000 {
		return 3
	}
	return 4
}

func (t *Transcoder) handle(u unit) error {
	switch {
	case u.noBOM:
		t.off += u.n
		t.term = ErrNoBOM
		return ErrNoBOM
	case u.bad:
		t.st.BadUnits++
		t.st.BadBytes += int64(u.n)
		if t.cfg.Strict {
			e := ErrIllegal
			if u.trunc {
				e = ErrTruncated
			}
			t.off += u.n
			t.term = &OffsetError{Err: e, Offset: t.cfg.Base + t.off - u.n, Len: u.n}
			return t.term
		}
		if !t.emit(u8.Replacement) {
			t.term = ErrLimit
			return ErrLimit
		}
	case t.first && u.r == 0xFEFF:
		t.first = false
		t.st.BOMBytes += int64(u.n)
		if t.cfg.KeepBOM && !t.emit(u.r) {
			t.term = ErrLimit
			return ErrLimit
		}
	default:
		t.first = false
		t.st.Scalars++
		t.st.ValidBytes += int64(u.n)
		if !t.emit(u.r) {
			t.term = ErrLimit
			return ErrLimit
		}
	}
	t.off += u.n
	return nil
}

// Write feeds bytes; n counts bytes committed to fully emitted units.
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.term != nil {
		return 0, t.term
	}
	start := t.off
	for _, b := range p {
		for _, u := range t.feed(b) {
			if err := t.handle(u); err != nil {
				return t.off - start, err
			}
		}
	}
	return t.off - start, nil
}

// Close flushes a trailing partial unit and becomes terminal.
func (t *Transcoder) Close() error {
	if t.term != nil {
		return t.term
	}
	for _, u := range t.finish() {
		if err := t.handle(u); err != nil {
			return err
		}
	}
	t.term = ErrClosed
	return nil
}
