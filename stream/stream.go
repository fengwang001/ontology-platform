// Package stream is a stateful, in-memory UTF-8 ⇄ UTF-16 transcoder.
//
// A single Transcoder is not safe for concurrent use.
package stream

import (
	"errors"
	"fmt"

	"ontology/u16"
	"ontology/u8"
)

// Encoding selects a wire format.
type Encoding uint8

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

// MaxCache is the hard upper bound on bytes held waiting for a unit.
const MaxCache = 3

// Config configures a Transcoder.
type Config struct {
	From, To  Encoding
	Strict    bool   // first invalid unit terminates the stream
	KeepInBOM bool   // false: a leading input BOM is consumed
	EmitBOM   bool   // true: prefix output with the output-form BOM
	MaxOut    int    // 0 = unlimited
}

// Stats are byte-conservation counters.
type Stats struct {
	Scalars   int64 // accepted legal scalars (BOM excluded)
	Invalid   int64 // invalid units
	BadBytes  int64 // bytes swallowed by invalid units
	BOMBytes  int64 // input bytes occupied by a leading BOM
	Consumed  int64 // total input bytes consumed
	Checks    int64 // total byte examinations
}

// Error kinds; all are mutually distinguishable with errors.Is.
var (
	ErrIllegal    = errors.New("stream: illegal byte")
	ErrTruncated  = errors.New("stream: input truncated")
	ErrLimit      = errors.New("stream: output limit exceeded")
	ErrTerminal   = errors.New("stream: write after terminal error")
)

// OffsetError reports a unit position in the whole input stream.
type OffsetError struct {
	Err    error
	Offset int64
	Len    int
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("%v at byte %d len %d", e.Err, e.Offset, e.Len)
}
func (e *OffsetError) Unwrap() error { return e.Err }

// Transcoder is a streaming transcoder.
type Transcoder struct {
	cfg      Config
	pend     []byte
	out      []byte
	stats    Stats
	started  bool
	closed   bool
	terminal error
	endian   u16.Endian
}

// New builds a Transcoder.
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, pend: make([]byte, 0, MaxCache)}
	if cfg.From == UTF16BE {
		t.endian = u16.BE
	}
	return t
}

// Write feeds input bytes. n counts bytes whose scalar has been fully emitted;
// bytes waiting in the split cache are not counted.
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal != nil {
		return 0, fmt.Errorf("%w: %v", ErrTerminal, t.terminal)
	}
	if t.closed {
		t.terminal = fmt.Errorf("%w: closed", ErrTerminal)
		return 0, t.terminal
	}
	base := t.stats.Consumed
	i := 0
	for i < len(p) {
		t.pend = append(t.pend, p[i])
		i++
		if !t.canAdvance() {
			continue
		}
		r, sz, kind := t.step()
		if kind == incomplete {
			continue
		}
		off := t.stats.Consumed
		unit := append([]byte(nil), t.pend[:sz]...)
		t.pend = t.pend[sz:]
		t.stats.Consumed += int64(sz)
		if err := t.commit(unit, r, kind, off); err != nil {
			t.stats.Consumed = base + int64(i) - int64(len(t.pend))
			return int(t.stats.Consumed - base), err
		}
	}
	t.stats.Consumed = base + int64(i) - int64(len(t.pend))
	return int(t.stats.Consumed - base), nil
}

const (
	kindOK = iota
	kindInvalid
	incomplete
)

func (t *Transcoder) canAdvance() bool {
	if t.cfg.From == UTF8 {
		return true
	}
	if t.cfg.From == UTF16LE { // auto-detect order from first two bytes
		if len(t.pend) == 2 && !t.started {
			if t.pend[0] == 0xFF && t.pend[1] == 0xFE {
				t.endian = u16.LE
			} else {
				t.endian = u16.BE
			}
		}
	}
	return len(t.pend) >= 2
}

func (t *Transcoder) step() (r rune, sz, kind int) {
	if t.cfg.From == UTF8 {
		rr, s, k := u8.Decode(t.pend)
		return rr, s, mapKind(k)
	}
	rr, s, k := u16.Decode(t.pend, t.endian)
	return rr, s, mapKind(k)
}

func mapKind(k interface{ String() string }) int { return 0 }

// Output returns the bytes emitted so far.
func (t *Transcoder) Output() []byte { return t.out }

// Stats returns a snapshot of the counters.
func (t *Transcoder) Stats() Stats { return t.stats }

// PendingLen exposes the current split-cache size.
func (t *Transcoder) PendingLen() int { return len(t.pend) }
