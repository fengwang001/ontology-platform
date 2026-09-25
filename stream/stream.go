// Package stream provides stateful streaming transcoders between UTF-8 and
// UTF-16 (LE/BE) with replace or strict error policy, output limits and
// per-stream statistics. A Transcoder is not safe for concurrent use.
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Direction selects the transcoding.
type Direction int

const (
	U8ToU16LE Direction = iota
	U8ToU16BE
	U16LEToU8
	U16BEToU8
	U8ToU8
)

// Config configures a Transcoder.
type Config struct {
	Dir       Direction
	Strict    bool
	MaxOutput int  // <= 0 means unlimited
	EmitBOM   bool // emit a BOM in the output encoding
}

// Stats are byte-conservation counters over the whole input stream.
type Stats struct {
	Runes     int64 // accepted legal scalars (excluding consumed BOM)
	BadUnits  int64 // illegal units
	BadBytes  int64 // bytes swallowed by illegal units / truncation
	BOMBytes  int64 // input bytes consumed as a leading BOM
	Consumed  int64 // total resolved input bytes
	Checks    int64 // total byte examinations
}

var (
	// ErrInvalidByte is the strict-mode sentinel; inspect *InvalidError.
	ErrInvalidByte = errors.New("stream: invalid byte unit")
	// ErrTruncated is returned by Close when a legal prefix is incomplete.
	ErrTruncated = errors.New("stream: truncated input")
	// ErrLimit is returned when the next scalar would exceed MaxOutput.
	ErrLimit = errors.New("stream: output limit exceeded")
	// ErrTerminal is returned by Write after a terminal error was set.
	ErrTerminal = errors.New("stream: transcoder in terminal state")
)

// InvalidError locates one illegal unit in the whole input stream.
type InvalidError struct {
	Offset int64
	Len    int
}

func (e *InvalidError) Error() string { return ErrInvalidByte.Error() }
func (e *InvalidError) Unwrap() error { return ErrInvalidByte }

// TruncatedError locates a truncated prefix at end of input.
type TruncatedError struct {
	Offset int64
	Len    int
}

func (e *TruncatedError) Error() string { return ErrTruncated.Error() }
func (e *TruncatedError) Unwrap() error { return ErrTruncated }

type feeder interface {
	feed(p []byte) (unit, int)
	flush() unit
	pending() []byte
	checks() int64
}

type unit struct {
	r        scalar.Rune
	len      int
	bad      bool
	trunc    bool
	bom      bool
}

// Transcoder streams one direction of conversion.
type Transcoder struct {
	cfg     Config
	f       feeder
	out     []byte
	stats   Stats
	fed     int64
	resolved int64
	term    error
	closed  bool
	bomOut  bool
}

// New builds a Transcoder from cfg.
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	switch cfg.Dir {
	case U8ToU16LE:
		t.f = &a8{d: &u8.Decoder{}}
	case U8ToU16BE:
		t.f = &a8{d: &u8.Decoder{}}
	case U16LEToU8:
		t.f = &a16{d: u16.NewDecoder(u16.LE)}
	case U16BEToU8:
		t.f = &a16{d: u16.NewDecoder(u16.BE)}
	default:
		t.f = &a8{d: &u8.Decoder{}}
	}
	return t
}

// Write feeds input bytes and returns how many of p were resolved into scalars.
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.term != nil {
		return 0, t.term
	}
	if t.closed {
		return 0, ErrTerminal
	}
	t.fed += int64(len(p))
	res0 := t.resolved
	off := 0
	for off < len(p) {
		u, used := t.f.feed(p[off:])
		off += used
		if u.len == 0 {
			continue
		}
		if err := t.commit(u, false); err != nil {
			t.term = err
			return int(t.resolved - res0), err
		}
		t.resolved += int64(u.len)
	}
	return len(p), nil
}

func (t *Transcoder) commit(u unit, atEnd bool) error {
	if u.bom {
		t.stats.BOMBytes += int64(u.len)
		if t.cfg.EmitBOM {
			if err := t.emit(scalar.BOM); err != nil {
				return err
			}
		}
		return nil
	}
	if u.bad {
		t.stats.BadUnits++
		t.stats.BadBytes += int64(u.len)
		if t.cfg.Strict {
			if u.trunc {
				return &TruncatedError{Offset: t.resolved, Len: u.len}
			}
			return &InvalidError{Offset: t.resolved, Len: u.len}
		}
		return t.emit(scalar.Replacement)
	}
	t.stats.Runes++
	return t.emit(u.r)
}

func (t *Transcoder) emit(r scalar.Rune) error {
	head := []byte(nil)
	if t.cfg.EmitBOM && !t.bomOut {
		head = t.encode(scalar.BOM)
	}
	enc := t.encode(r)
	need := len(head) + len(enc)
	if t.cfg.MaxOutput > 0 && len(t.out)+need > t.cfg.MaxOutput {
		return ErrLimit
	}
	if head != nil {
		t.bomOut = true
		t.out = append(t.out, head...)
	}
	t.out = append(t.out, enc...)
	return nil
}

// Close flushes any truncated prefix.
func (t *Transcoder) Close() error {
	if t.term != nil {
		return t.term
	}
	t.closed = true
	u := t.f.flush()
	if u.len == 0 {
		return nil
	}
	if err := t.commit(u, true); err != nil {
		t.term = err
		return err
	}
	t.resolved += int64(u.len)
	return nil
}

// Output returns all emitted output bytes so far.
func (t *Transcoder) Output() []byte { return t.out }

// Stats returns the conservation counters.
func (t *Transcoder) Stats() Stats {
	s := t.stats
	s.Consumed = t.resolved
	s.Checks = t.f.checks()
	return s
}

// Unread returns the unresolved buffered tail (0..MaxPending bytes) after a
// limit error; feed it to a fresh Transcoder to resume.
func (t *Transcoder) Unread() []byte { return t.f.pending() }

func (t *Transcoder) encode(r scalar.Rune) []byte {
	switch t.cfg.Dir {
	case U8ToU16LE:
		return u16.Encode(nil, r, u16.LE)
	case U8ToU16BE:
		return u16.Encode(nil, r, u16.BE)
	default:
		return u8.Encode(nil, r)
	}
}

type a8 struct {
	d *u8.Decoder
}

func (a *a8) feed(p []byte) (unit, int) {
	u, n := a.d.Feed(p)
	return unit{r: u.R, len: u.Len, bad: u.Bad}, n
}
func (a *a8) flush() unit {
	u := a.d.Flush()
	return unit{r: u.R, len: u.Len, bad: u.Bad, trunc: u.Bad}
}
func (a *a8) pending() []byte { return a.d.Pending() }
func (a *a8) checks() int64   { return a.d.Checks }

type a16 struct{ d *u16.Decoder }

func (a *a16) feed(p []byte) (unit, int) {
	u, n := a.d.Feed(p)
	return unit{r: u.R, len: u.Len, bad: u.Bad, trunc: u.Trunc, bom: u.BOM}, n
}
func (a *a16) flush() unit {
	u := a.d.Flush()
	return unit{r: u.R, len: u.Len, bad: u.Bad, trunc: u.Trunc}
}
func (a *a16) pending() []byte {
	b := make([]byte, 0, u16.MaxPending)
	if u := a.d.PendingBytes(); u != nil {
		b = append(b, u...)
	}
	return b
}
func (a *a16) checks() int64 { return a.d.Checks }
