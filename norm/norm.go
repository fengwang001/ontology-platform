// Package norm is a streaming normalizer for line endings and trailing
// whitespace. It wires eol, ws and span together, applies the trailing
// newline policy on Close, and enforces byte limits.
//
// A Normalizer is not safe for concurrent use.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Strategy selects the trailing-newline policy.
type Strategy int

const (
	KeepOriginal Strategy = iota // leave final newlines untouched
	EnsureOne                    // collapse final newlines to exactly one
	Trim                         // drop final blank lines, keep one newline
)

// Config configures a Normalizer. Zero MaxSpace/MaxOutput means unlimited.
type Config struct {
	Ending    Strategy
	StrictNUL bool
	MaxSpace  int
	MaxOutput int
}

var (
	// ErrSpaceLimit: provisional whitespace run exceeded MaxSpace.
	ErrSpaceLimit = errors.New("norm: trailing whitespace buffer limit exceeded")
	// ErrOutputLimit: normalized output exceeded MaxOutput.
	ErrOutputLimit = errors.New("norm: output size limit exceeded")
	// ErrNUL: a NUL byte arrived in strict mode.
	ErrNUL = errors.New("norm: NUL byte in strict mode")
	// ErrClosed: Write after a terminal error or Close.
	ErrClosed = errors.New("norm: normalizer is in terminal state")
)

// OffsetError carries a sentinel cause and the original-stream offset.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer is a streaming writer. The zero value uses KeepOriginal.
type Normalizer struct {
	cfg       Config
	sc        eol.Scanner
	buf       ws.Buf
	sb        span.Builder
	out       []byte
	off       int
	closed    bool
	terminal  error
	lastByte  byte
	hasByte   bool
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

// Write feeds one chunk; results are independent of chunk boundaries.
func (n *Normalizer) Write(p []byte) (int, error) {
	for k, b := range p {
		if err := n.feed(b); err != nil {
			return k, err
		}
	}
	return len(p), nil
}

func (n *Normalizer) fail(err error) error {
	n.terminal = err
	n.closed = true
	return err
}

func (n *Normalizer) feed(b byte) error {
	if n.closed {
		return ErrClosed
	}
	pos := n.off
	n.off++
	if n.cfg.StrictNUL && b == 0 {
		return n.fail(&OffsetError{Err: ErrNUL, Offset: pos})
	}
	extra, lf := n.sc.Feed(b)
	if extra {
		n.resolveEnding(pos)
	}
	if lf {
		if b == '\n' {
			n.emitEnding(pos)
		}
		return nil
	}
	if !extra && b != '\r' && b != '\n' {
		if ws.Space(b) {
			n.buf.Add(b)
			if n.cfg.MaxSpace > 0 && n.buf.Len() > n.cfg.MaxSpace {
				return n.fail(&OffsetError{Err: ErrSpaceLimit, Offset: pos})
			}
			return nil
		}
		if n.buf.Pending() {
			n.flushInterior()
		}
		n.emitByte(b)
		n.lastByte, n.hasByte = b, true
	}
	return nil
}

func (n *Normalizer) resolveEnding(pos int) {
	if n.buf.Pending() {
		n.sb.Del(n.buf.Len())
		n.buf.Trailing()
	}
	n.emitLF()
	n.lastByte, n.hasByte = '\n', true
}

func (n *Normalizer) emitEnding(pos int) {
	if n.buf.Pending() {
		n.sb.Del(n.buf.Len())
		n.buf.Trailing()
	}
	n.emitLF()
	n.lastByte, n.hasByte = '\n', true
}

func (n *Normalizer) emitLF() {
	if n.cfg.MaxOutput > 0 && len(n.out)+1 > n.cfg.MaxOutput {
		n.fail(&OffsetError{Err: ErrOutputLimit, Offset: n.off})
		return
	}
	n.out = append(n.out, '\n')
	n.sb.Replace()
}

func (n *Normalizer) flushInterior() {
	d := n.buf.Interior()
	n.growOut(d)
	n.sb.Keep(len(d))
}

func (n *Normalizer) emitByte(b byte) {
	if n.cfg.MaxOutput > 0 && len(n.out)+1 > n.cfg.MaxOutput {
		n.fail(&OffsetError{Err: ErrOutputLimit, Offset: n.off})
		return
	}
	n.out = append(n.out, b)
	n.sb.Keep(1)
}

func (n *Normalizer) growOut(p []byte) {
	if n.cfg.MaxOutput > 0 && len(n.out)+len(p) > n.cfg.MaxOutput {
		n.fail(&OffsetError{Err: ErrOutputLimit, Offset: n.off})
		return
	}
	n.out = append(n.out, p...)
}

// Close finalizes the stream. A pending '\r' or whitespace run is
// resolved exactly as if the full input had been seen.
func (n *Normalizer) Close() error {
	if n.terminal != nil {
		return n.terminal
	}
	if n.closed {
		return ErrClosed
	}
	n.closed = true
	if n.sc.Close() {
		n.resolveEnding(n.off)
	}
	if n.buf.Pending() {
		if n.cfg.Ending == Trim {
			n.sb.Del(n.buf.Len())
			n.buf.Trailing()
		} else {
			n.flushInterior()
	}
	}
	if err := n.applyEnding(); err != nil {
		n.terminal = err
		return err
	}
	return nil
}

func (n *Normalizer) applyEnding() error {
	if n.cfg.Ending == KeepOriginal {
		return nil
	}
	i := len(n.out)
	for i > 0 && n.out[i-1] == '\n' {
		i--
	}
	if n.cfg.Ending == Trim && i == 0 {
		n.sb.TruncateOut(0)
		n.out = n.out[:0]
		return nil
	}
	if i == len(n.out) {
		if n.cfg.MaxOutput > 0 && len(n.out)+1 > n.cfg.MaxOutput {
			err := &OffsetError{Err: ErrOutputLimit, Offset: n.off}
			n.terminal = err
			return err
		}
		n.out = append(n.out, '\n')
		n.sb.Ins(1)
		return nil
	}
	n.sb.TruncateOut(i + 1)
	n.out = append(n.out[:i+1])
	return nil
}

// Output returns the finalized normalized bytes.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the finalized offset map.
func (n *Normalizer) Map() *span.Map { return n.sb.Build() }

// PendingCR reports an unresolved trailing '\r' (par handoff).
func (n *Normalizer) PendingCR() bool { return n.sc.Pending() }

// PendingSpace returns any buffered provisional trailing whitespace.
func (n *Normalizer) PendingSpace() []byte { return n.buf.Bytes() }

// TermErr reports the terminal error, if any.
func (n *Normalizer) TermErr() error { return n.terminal }
