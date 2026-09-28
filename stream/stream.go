// Package stream implements streaming UTF-8 ⇄ UTF-16 transcoding with either
// strict failure or U+FFFD replacement for ill-formed units.
//
// A single Transcoder is not safe for concurrent use by multiple goroutines.
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Direction selects the input/output encodings.
type Direction int

const (
	U8ToU8 Direction = iota
	U8ToU16LE
	U8ToU16BE
	U16LEToU8
	U16BEToU8
)

// Config configures a Transcoder.
type Config struct {
	Dir       Direction
	Strict    bool
	MaxOutput int  // 0 = unlimited
	EmitBOM   bool // emit an initial U+FEFF; input BOM is always consumed
	NoBOMScan bool // do not recognize a leading BOM (used by par workers)
}

// Stats are byte-conservation counters.
type Stats struct {
	Scalars    int
	BadUnits   int
	BadBytes   int
	BOMBytes   int
	Consumed   int
	Checks     int
	Truncated  bool
}

var (
	// ErrInvalidByte is an ill-formed byte unit. See *OffsetError.
	ErrInvalidByte = errors.New("stream: invalid byte unit")
	// ErrTruncated is an incomplete unit at end of input.
	ErrTruncated = errors.New("stream: truncated input")
	// ErrOutputLimit is returned when MaxOutput would be crossed.
	ErrOutputLimit = errors.New("stream: output limit exceeded")
	// ErrClosed is returned by Write after the transcoder has terminated.
	ErrClosed = errors.New("stream: transcoder terminated")
)

// OffsetError wraps an error with its unit's absolute start offset and size.
type OffsetError struct {
	Err    error
	Offset int
	Size   int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

const maxPending = 3 // UTF-8 <=3 bytes; UTF-16 <=3 (half + high surrogate)

// Transcoder is a stateful streaming transcoder.
type Transcoder struct {
	cfg     Config
	out     []byte
	stats   Stats
	termErr error
	closed  bool
	started bool
	dec8    u8.Decoder
	dec16   u16.Decoder
	bomBuf  []byte
	unitStart int
}

// New creates a Transcoder.
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, bomBuf: make([]byte, 0, 3)}
	if cfg.Dir == U16LEToU8 {
		t.dec16 = *u16.NewDecoder(false)
	} else {
		t.dec16 = *u16.NewDecoder(true)
	}
	return t
}

// Output returns the bytes produced so far.
func (t *Transcoder) Output() []byte { return t.out }

// Stats returns the accounting counters.
func (t *Transcoder) Stats() Stats { return t.stats }

// PendingLen returns bytes held in an incomplete prefix at the end.
func (t *Transcoder) PendingLen() int {
	if t.cfg.Dir == U8ToU8 {
		if t.dec8.Pending() {
			return t.dec8.PendingLen()
		}
		return 0
	}
	return t.dec16.PendingLen()
}

// Checks returns the number of times input bytes were examined.
func (t *Transcoder) Checks() int { return t.stats.Checks }

// Err reports the terminal error, if any.
func (t *Transcoder) Err() error { return t.termErr }
