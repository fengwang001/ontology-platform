// Package norm is a streaming line-ending and trailing-whitespace
// normalizer with a bidirectional original/output offset map.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Ending selects the trailing-newline policy.
type Ending int

const (
	Preserve   Ending = iota // leave the tail as normalized
	EnsureOne                // non-empty output ends with exactly one '\n'
	TrimBlanks               // drop trailing blank lines; keep one '\n' on content
)

// Config configures a Normalizer. Zero MaxWSLen/MaxOut mean unlimited.
type Config struct {
	Ending   Ending
	Strict   bool // reject NUL bytes
	MaxWSLen int  // max buffered trailing-whitespace run
	MaxOut   int  // max emitted output bytes
}

var (
	ErrNUL             = errors.New("norm: NUL byte in strict mode")
	ErrWhitespaceLimit = errors.New("norm: trailing whitespace buffer limit exceeded")
	ErrOutputLimit     = errors.New("norm: output size limit exceeded")
	ErrClosed          = errors.New("norm: write after terminal state")
)

// OffsetError attaches the original byte offset where an error occurred.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer normalizes a byte stream. A single instance is not safe for
// concurrent use.
type Normalizer struct {
	cfg         Config
	dec         eol.Decoder
	run         ws.Run
	map_        span.Mapper
	out         []byte
	origPos     int
	closed      bool
	failed      bool
	hasNonBlank bool
}

// New returns a Normalizer with the given configuration.
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

func (n *Normalizer) fail(err error, off int) error {
	n.failed = true
	return &OffsetError{Err: err, Offset: off}
}

func (n *Normalizer) emit(b byte) error {
	if n.cfg.MaxOut > 0 && len(n.out) >= n.cfg.MaxOut {
		return n.fail(ErrOutputLimit, n.origPos)
	}
	n.out = append(n.out, b)
	n.map_.Copy(1)
	return nil
}

// flushPending resolves a buffered whitespace run: atEOL deletes it,
// otherwise it is interior whitespace and emitted verbatim.
func (n *Normalizer) flushPending(atEOL bool) error {
	if !n.run.Active() {
		return nil
	}
	buf := n.run.Take()
	if atEOL {
		n.map_.Delete(len(buf))
		return nil
	}
	for _, b := range buf {
		if err := n.emit(b); err != nil {
			return err
		}
	}
	return nil
}

func (n *Normalizer) emitLF() error {
	if err := n.flushPending(true); err != nil {
		return err
	}
	if err := n.emit('\n'); err != nil {
		return err
	}
	n.dec.Flush()
	return nil
}

// Write feeds one chunk. Splitting the input at arbitrary byte offsets
// yields byte-identical output and map.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.failed {
		return 0, ErrClosed
	}
	for i, b := range p {
		off := n.origPos
		n.origPos++
		switch r := n.dec.Step(b); r {
		case eol.CRBeforeLF:
			n.map_.Delete(1) // the '\r' of CRLF
			if err := n.emitLF(); err != nil {
				return i, err
			}
		case eol.LoneLF:
			if err := n.emitLF(); err != nil {
				return i, err
			}
		case eol.CROverCR:
			if err := n.emitLF(); err != nil {
				return i, err
			}
		case eol.LFLongPendingCR:
			if err := n.emitLF(); err != nil {
				return i, err
			}
			if err := n.emitLF(); err != nil {
				return i, err
			}
		case eol.OtherLongPendingCR:
			if err := n.emitLF(); err != nil {
				return i, err
			}
			if err := n.consume(b, off); err != nil {
				return i, err
			}
		default:
			if err := n.consume(b, off); err != nil {
				return i, err
			}
		}
	}
	return len(p), nil
}

func (n *Normalizer) consume(b byte, off int) error {
	switch {
	case ws.IsSpace(byte(b)):
		if n.cfg.MaxWSLen > 0 && n.run.Len() >= n.cfg.MaxWSLen {
			return n.fail(ErrWhitespaceLimit, n.run.Start)
		}
		n.run.Add(b, off)
	case n.cfg.Strict && b == 0:
		return n.fail(ErrNUL, off)
	default:
		n.hasNonBlank = true
		if err := n.flushPending(false); err != nil {
			return err
		}
		return n.emit(b)
	}
	return nil
}

// Close resolves any pending state and applies the ending policy. It is
// idempotent; writes after Close return ErrClosed.
func (n *Normalizer) Close() error {
	if n.closed || n.failed {
		return ErrClosed
	}
	n.closed = true
	if n.dec.Flush() {
		if err := n.emitLF(); err != nil {
			n.failed = true
			return err
		}
	}
	if err := n.flushPending(true); err != nil {
		n.failed = true
		return err
	}
	switch n.cfg.Ending {
	case EnsureOne:
		if len(n.out) > 0 {
			n.trimTrailingLF()
			n.out = append(n.out, '\n')
			n.map_.Insert(1)
		}
	case TrimBlanks:
		if n.hasNonBlank {
			n.trimTrailingLF()
			n.out = append(n.out, '\n')
			n.map_.Insert(1)
		}
	}
	return nil
}

// trimTrailingLF removes every trailing '\n' from the output and map.
func (n *Normalizer) trimTrailingLF() {
	u := len(n.out)
	for u > 0 && n.out[u-1] == '\n' {
		u--
	}
	if u == len(n.out) {
		return
	}
	n.map_.Truncate(u)
	n.out = n.out[:u]
}

// Output returns the normalized bytes produced so far.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the bidirectional offset mapper.
func (n *Normalizer) Map() *span.Mapper { return &n.map_ }

// Run normalizes a whole buffer in one shot and returns the output and map.
func Run(p []byte, cfg Config) ([]byte, *span.Mapper, error) {
	n := New(cfg)
	if _, err := n.Write(p); err != nil {
		return n.Output(), n.Map(), err
	}
	if err := n.Close(); err != nil {
		return n.Output(), n.Map(), err
	}
	return n.Output(), n.Map(), nil
}
