// Package stream implements a streaming UTF-8 <-> UTF-16 transcoder.
package stream

import (
	"errors"
	"fmt"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Encoding int // byte encoding selector

const UTF8, UTF16LE, UTF16BE Encoding = 0, 1, 2
// Config configures a Transcoder. Resume continues a previous stream: no BOM handling.
type Config struct {
	From, To                Encoding
	Strict, EmitBOM, Resume bool
	MaxOut                  int // output byte limit, 0 = unlimited
}

// Stats is the consumption accounting; ScalarBytes+InvalidBytes+BOMBytes == Consumed.
type Stats struct{ Scalars, ScalarBytes, InvalidUnits, InvalidBytes, BOMBytes, Consumed, Pending int }
var (
	ErrInvalid   = errors.New("stream: invalid byte sequence")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output limit exceeded")
	ErrClosed    = errors.New("stream: transcoder is terminal")
)
// Error is a positioned stream error; Unwrap yields ErrInvalid or ErrTruncated.
type Error struct {
	Err         error
	Offset, Len int
}
func (e *Error) Error() string {
	return fmt.Sprintf("stream: %v at offset %d (len %d)", e.Err, e.Offset, e.Len)
}
func (e *Error) Unwrap() error { return e.Err }
// Transcoder is a stateful streaming transcoder; not safe for concurrent use.
type Transcoder struct {
	cfg     Config
	out     []byte
	err     error
	bomDone bool
	st      Stats
	checked int
	d8      u8.Stepper
	d16     u16.Stepper
	step    func(byte, func(rune, int, bool) bool)
	flush   func(func(rune, int, bool) bool)
	pend    func() int
}
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, bomDone: cfg.Resume}
	t.d16 = u16.NewStepper(cfg.From == UTF16BE, !cfg.Resume)
	t.step, t.flush, t.pend = t.d8.Step, t.d8.Flush, t.d8.Pending
	if cfg.From != UTF8 {
		t.step, t.flush, t.pend = t.d16.Step, t.d16.Flush, t.d16.Pending
	}
	if cfg.EmitBOM && !cfg.Resume {
		t.out = append(t.out, [3][]byte{{0xEF, 0xBB, 0xBF}, {0xFF, 0xFE}, {0xFE, 0xFF}}[cfg.To]...)
	}
	return t
}

// Write consumes p and returns how many bytes of p were folded into completed units.
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.err != nil {
		return 0, t.err
	}
	base := t.st.Consumed + t.pend()
	for i := 0; i < len(p); i++ {
		t.checked++
		t.step(p[i], t.commit)
		if t.err != nil {
			return t.st.Consumed - base, t.err
		}
	}
	return t.st.Consumed - base, nil
}

// Close flushes a truncated tail or, in strict mode, fails with a TruncateError.
func (t *Transcoder) Close() error {
	if t.err != nil {
		return t.err
	}
	if n := t.pend(); n > 0 && t.cfg.Strict {
		t.err = &Error{Err: ErrTruncated, Offset: t.st.Consumed, Len: n}
		return t.err
	}
	t.flush(t.commit)
	if t.err != nil {
		return t.err
	}
	t.err = ErrClosed
	return nil
}

// commit accounts one completed unit; it reports false on terminal error.
func (t *Transcoder) commit(cp rune, size int, valid bool) bool {
	if !valid {
		t.bomDone = true
		if t.cfg.Strict {
			t.err = &Error{Err: ErrInvalid, Offset: t.st.Consumed, Len: size}
			return false
		}
		cp = scalar.Replacement
	}
	if !t.bomDone {
		t.bomDone = true
		if cp == scalar.BOM {
			t.st.BOMBytes, t.st.Consumed = t.st.BOMBytes+size, t.st.Consumed+size
			return true
		}
	}
	var buf [4]byte
	var n int
	if t.cfg.To == UTF8 {
		n = u8.Encode(buf[:], cp)
	} else {
		n = u16.Encode(buf[:], cp, t.cfg.To == UTF16BE)
	}
	if t.cfg.MaxOut > 0 && len(t.out)+n > t.cfg.MaxOut {
		t.err = ErrLimit
		return false
	}
	t.out = append(t.out, buf[:n]...)
	if valid {
		t.st.Scalars, t.st.ScalarBytes = t.st.Scalars+1, t.st.ScalarBytes+size
	} else {
		t.st.InvalidUnits, t.st.InvalidBytes = t.st.InvalidUnits+1, t.st.InvalidBytes+size
	}
	t.st.Consumed += size
	return true
}

// Output returns the transcoded bytes produced so far.
func (t *Transcoder) Output() []byte { return t.out }

// Stats returns the current consumption accounting.
func (t *Transcoder) Stats() Stats { t.st.Pending = t.pend(); return t.st }

// Checked returns the total number of input-byte inspections.
func (t *Transcoder) Checked() int { return t.checked }
