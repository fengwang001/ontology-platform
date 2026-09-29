// Package stream provides a streaming UTF-8 <-> UTF-16 transcoder with
// invalid-byte replacement. A Transcoder is NOT safe for concurrent
// use; use package par for parallel work.
package stream

import (
	"errors"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Format int

const (
	UTF8 Format = iota
	UTF16LE
	UTF16BE
)

var (
	ErrInvalid     = errors.New("stream: invalid byte unit")
	ErrTruncated   = errors.New("stream: truncated input")
	ErrOutputLimit = errors.New("stream: output limit exceeded")
	ErrTerminal    = errors.New("stream: write after terminal state")
)

// UnitError locates one invalid/truncated unit. Kind is ErrInvalid or
// ErrTruncated; use errors.Is to distinguish.
type UnitError struct {
	Kind   error
	Offset int64
	Len    int
}

func (e *UnitError) Error() string { return e.Kind.Error() }
func (e *UnitError) Unwrap() error { return e.Kind }

// Config configures a Transcoder. Interior makes Close leftovers count
// as invalid units instead of truncation (par non-final segments).
type Config struct {
	From, To        Format
	Strict, KeepBOM bool
	Limit           int
	Interior        bool
	// OnUnit, when set, is called for every invalid/truncated unit.
	OnUnit func(kind error, offset int64, size int)
}

type Stats struct {
	Runes, Invalid, BadBytes, BOMBytes, Consumed, Checks int64
}

// MaxPending is the hard cache limit for buffered valid-prefix bytes.
const MaxPending = 3

type pusher interface {
	Push(byte) int
	Rune() rune
	Reset()
}

type Transcoder struct {
	cfg               Config
	dec               pusher
	little            bool
	out, pend, replay []byte
	bom               []byte // leading-BOM candidate; len < bom length
	bomDone           bool
	stats             Stats
	prefix            int64
	terminal          error
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, little: cfg.From == UTF16LE}
	if cfg.From == UTF8 {
		t.dec = &u8.Decoder{}
	}
	return t
}

func (t *Transcoder) Stats() Stats   { return t.stats }
func (t *Transcoder) Output() []byte { return t.out }

// Pending returns bytes already handed to Write but not yet part of a
// complete unit. After an output-limit stop, resume a fresh instance
// with append(old.Pending(), p[n:]...).
func (t *Transcoder) Pending() []byte { return append([]byte{}, t.pend...) }

func (t *Transcoder) emit(r rune) error {
	old := len(t.out)
	if t.cfg.To == UTF8 {
		t.out = u8.Encode(t.out, r)
	} else {
		t.out = u16.Encode(t.out, r, t.cfg.To == UTF16LE)
	}
	if t.cfg.Limit > 0 && len(t.out) > t.cfg.Limit {
		t.out = t.out[:old]
		return ErrOutputLimit
	}
	return nil
}

func (t *Transcoder) unit(kind error, off int64, n int) error {
	t.stats.Invalid++
	t.stats.BadBytes += int64(n)
	if t.cfg.OnUnit != nil {
		t.cfg.OnUnit(kind, off, n)
	}
	if t.cfg.Strict {
		return &UnitError{Kind: kind, Offset: off, Len: n}
	}
	return t.emit(scalar.Replacement)
}

// Write feeds bytes. n is the number of bytes scanned; bytes belonging
// to no complete unit stay available via Pending.
func (t *Transcoder) Write(p []byte) (n int, err error) {
	if t.terminal != nil {
		return 0, ErrTerminal
	}
	src := append(append([]byte{}, t.replay...), p...)
	t.replay = nil
	baseReplay := len(src) - len(p)
	for n < len(src) {
		t.stats.Checks++
		off := t.prefix + int64(n-baseReplay)
		if !t.bomDone {
			adv, done, e := t.probe(src[n:])
			n += adv
			if e != nil {
				return t.stop(n, baseReplay, e)
			}
			if done {
				continue
			}
			break
		}
		if t.dec == nil {
			t.dec = u16.NewDecoder(t.little)
		}
		b := src[n]
		switch t.dec.Push(b) {
		case u8.NeedMore:
			t.pend = append(t.pend, b)
			n++
		case u8.RuneReady:
			size := 1 + len(t.pend)
			if e := t.emit(t.dec.Rune()); e != nil {
				t.pend = append(t.pend, b)
				return t.stop(n, baseReplay, e)
			}
			t.pend = nil
			t.stats.Runes++
			t.stats.Consumed += int64(size)
			n++
		case u8.InvalidByte:
			t.pend = nil
			if e := t.unit(ErrInvalid, off, 1); e != nil {
				return t.stop(n, baseReplay, e)
			}
			n++
		default: // InvalidPrefix
			size := len(t.pend)
			uoff := off - int64(size)
			t.replay = append(t.replay, b)
			t.pend = nil
			t.dec.Reset()
			if e := t.unit(ErrInvalid, uoff, size); e != nil {
				return t.stop(n, baseReplay, e)
			}
			n++
		}
	}
	t.prefix += int64(len(p))
	r := n - baseReplay
	if r < 0 {
		r = 0
	}
	return r, nil
}

func (t *Transcoder) stop(scanned, baseReplay int, e error) (int, error) {
	t.prefix += int64(scanned - baseReplay)
	t.terminal = e
	return scanned - baseReplay, e
}
