// Package stream provides streaming UTF-8/UTF-16 transcoding with illegal
// byte replacement. A Transcoder is not safe for concurrent use.
package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

// Encoding selectors.
const (
	UTF8    = iota
	UTF16LE
	UTF16BE
	UTF16Auto
)

var (
	// ErrIllegal is an illegal unit; *Error carries offset and length.
	ErrIllegal = errors.New("stream: illegal unit")
	// ErrTruncated is an unfinished prefix at end of input; *Error has offset.
	ErrTruncated = errors.New("stream: truncated input")
	// ErrLimit means the output byte cap would be crossed.
	ErrLimit = errors.New("stream: output limit reached")
	// ErrClosedWrite is a write after the terminal state.
	ErrClosedWrite = errors.New("stream: write after terminal error")
)

// Error carries the start offset and swallowed length of a unit.
type Error struct {
	Kind   error
	Offset int
	Len    int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

// Stats are byte-conservation counters.
type Stats struct {
	Scalars int64 // accepted valid scalars
	Illegal int64 // illegal units emitted/reported
	Swallow int64 // bytes swallowed by illegal units
	BOMBytes int64 // bytes occupied by the leading BOM
	Consumed int64 // finalized input bytes (scalars + swallow + BOM)
	Checks   int64 // times bytes were examined
}

// Config configures a Transcoder.
type Config struct {
	From       int
	To         int
	Strict     bool
	MaxOut     int    // 0 means unlimited
	EmitBOM    bool   // keep the leading BOM in the output
	StartOff   int    // absolute offset of the first byte
	HandleBOM  bool   // recognize/strip a leading BOM (false for par tail workers)
}

// Transcoder streams bytes into encoded output.
type Transcoder struct {
	cfg      Config
	out      []byte
	dec8     u8.Decoder
	dec16    u16.Decoder
	closed   bool
	fatal    error
	stats    Stats
	carry    []byte // un-acked bytes from a previous Write (limit stop)
	orderOut int
}

// New builds a Transcoder.
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	t.dec16 = *u16.NewDecoder(cfg.outOrder(), cfg.From == UTF16Auto)
	t.dec16.SetStart(cfg.StartOff)
	return t
}

func (c Config) outOrder() int {
	if c.To == UTF16BE {
		return u16.BE
	}
	return u16.LE
}

// Pending returns carry bytes not acked after an ErrLimit stop; the caller
// re-feeds them ahead of the rest into a fresh instance.
func (t *Transcoder) Pending() []byte {
	out := make([]byte, len(t.carry))
	copy(out, t.carry)
	return out
}

// Output returns the accumulated output bytes.
func (t *Transcoder) Output() []byte { return t.out }

// Stats returns a copy of the counters.
func (t *Transcoder) Stats() Stats { return t.stats }
