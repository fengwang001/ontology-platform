// Package stream provides streaming UTF-8 <-> UTF-16 transcoding with illegal
// byte replacement, BOM handling, strict mode and an output byte ceiling.
//
// A single Transcoder is not safe for concurrent use.
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Encoding selects a byte encoding.
type Encoding int

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
	UTF16Auto
)

// Sentinel errors; classify with errors.Is.
var (
	ErrIllegal   = errors.New("stream: illegal unit")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output limit exceeded")
	ErrTerminal  = errors.New("stream: write after terminal state")
)

// OffsetError carries the global start offset and byte size of a unit.
type OffsetError struct {
	Kind         error
	Offset       int64
	Size         int
}

func (e *OffsetError) Error() string { return e.Kind.Error() }
func (e *OffsetError) Unwrap() error { return e.Kind }

// Stats are byte-conservation counters.
type Stats struct {
	Scalars  int64
	BadUnits int64
	BadBytes int64
	BOMBytes int64
	Consumed int64
	Checks   int64
}

// Config configures a Transcoder.
type Config struct {
	From, To Encoding
	Strict   bool
	EmitBOM  bool
	MaxOut   int64
}

// Transcoder is a resumable, non-concurrent streaming transcoder.
type Transcoder struct {
	cfg    Config
	d8     u8.Decoder
	d16    *u16.Decoder
	out    []byte
	cons   int64
	st     Stats
	term   error
	closed bool
	first  bool
}

// New creates a Transcoder.
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, first: true}
	if cfg.From != UTF8 {
		e := u16.Auto
		if cfg.From == UTF16LE {
			e = u16.LE
		} else if cfg.From == UTF16BE {
			e = u16.BE
		}
		t.d16 = u16.NewDecoder(e)
	}
	return t
}

// Output returns emitted bytes.
func (t *Transcoder) Output() []byte { return t.out }

// Stats returns a counters snapshot with Consumed filled in.
func (t *Transcoder) Stats() Stats {
	s := t.st
	s.Consumed = t.cons
	return s
}

// Checks returns the total input-byte inspection count.
func (t *Transcoder) Checks() int64 { return t.st.Checks }

// Pending returns buffered unresolved bytes.
func (t *Transcoder) Pending() int {
	if t.cfg.From == UTF8 {
		return t.d8.Pending()
	}
	return t.d16.Pending()
}

// Terminal reports the terminal error, if any.
func (t *Transcoder) Terminal() error { return t.term }

func (t *Transcoder) encode(r scalar.Rune) []byte {
	if t.cfg.To == UTF8 {
		return u8.Encode(nil, r)
	}
	e := u16.LE
	if t.cfg.To == UTF16BE {
		e = u16.BE
	}
	return u16.Encode(nil, e, r)
}
