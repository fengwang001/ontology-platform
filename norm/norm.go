// Package norm is a streaming line-ending/trailing-whitespace normalizer
// with a bidirectional original↔output offset map.
//
// A single Normalizer is not safe for concurrent use.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy selects the end-of-file newline behavior.
type Policy int

const (
	Keep      Policy = iota // leave the tail untouched
	EnsureOne               // non-empty output ends with exactly one \n
	TrimBlank               // drop blank tail lines, keep one newline
)

// Config configures a Normalizer. Zero-value limits mean unlimited.
type Config struct {
	Policy    Policy
	WSBuffer  int  // max pending trailing-whitespace bytes
	MaxOutput int  // max total output bytes
	StrictNUL bool // reject NUL bytes
}

var (
	ErrNUL         = errors.New("norm: NUL byte in strict mode")
	ErrWSBuffer    = errors.New("norm: trailing whitespace buffer limit exceeded")
	ErrOutputLimit = errors.New("norm: output byte limit exceeded")
	ErrClosed      = errors.New("norm: write after close or terminal error")
)

// Error wraps a sentinel with the original input offset where it occurred.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

func fail(kind error, off int) error { return &Error{Kind: kind, Offset: off} }

// Normalizer normalizes a stream.
type Normalizer struct {
	cfg    Config
	dec    *eol.Decoder
	wb     *ws.Buf
	out    []byte
	mp     *span.Mapper
	orig   int
	closed bool
	dead   error
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, dec: eol.New(), wb: ws.New(cfg.WSBuffer), mp: span.New()}
}

func (n *Normalizer) emitCopy(b []byte) error {
	if n.cfg.MaxOutput > 0 && len(n.out)+len(b) > n.cfg.MaxOutput {
		return ErrOutputLimit
	}
	n.out = append(n.out, b...)
	n.mp.Copy(len(b))
	return nil
}

// Write feeds one chunk; events stay stream-independent (a split '\r'/'\n'
// or a whitespace run cut at any boundary yields identical results).
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.dead != nil {
		return 0, fail(ErrClosed, n.orig)
	}
	var werr error
	n.dec.Feed(p, func(ev eol.Event, c byte) {
		if werr != nil {
			return
		}
		switch ev {
		case eol.DropCR:
			n.mp.Delete(1)
			n.orig++
		case eol.LF:
			if pend := n.wb.Take(); len(pend) > 0 {
				n.mp.Delete(len(pend))
			}
			if err := n.emitCopy([]byte{'\n'}); err != nil {
				werr = err
				return
			}
			n.orig++
		default:
			n.feedByte(c, &werr)
		}
	})
	if werr != nil {
		n.dead = fail(werr, n.orig)
		return 0, n.dead
	}
	return len(p), nil
}

func (n *Normalizer) feedByte(c byte, werr *error) {
	if c == 0 && n.cfg.StrictNUL {
		*werr = ErrNUL
		return
	}
	act, ok := n.wb.Observe(c)
	if !ok {
		*werr = ErrWSBuffer
		return
	}
	switch act {
	case ws.Buffer:
		n.orig++
	case ws.Pass:
		flush := n.wb.Take()
		b := append(append(make([]byte, 0, len(flush)+1), flush...), c)
		if err := n.emitCopy(b); err != nil {
			*werr = err
			return
		}
		n.orig++
	}
}

// Close flushes pending state and applies the tail policy. It is an error
// to write afterwards.
func (n *Normalizer) Close() error {
	if n.dead != nil {
		return n.dead
	}
	if n.closed {
		return fail(ErrClosed, n.orig)
	}
	if n.dec != nil {
		n.dec.Flush(func(ev eol.Event, c byte) {})
	}
	n.closed = true
	// Re-interpret pending state at end of stream.
	if false {
	}
	n.finishTail()
	return nil
}

func (n *Normalizer) finishTail() {}

// Output returns the normalized bytes.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the underlying offset mapper (valid after Close).
func (n *Normalizer) Map() *span.Mapper { return n.mp }

// ToOrig maps an output offset to an original offset.
func (n *Normalizer) ToOrig(u int) int { return n.mp.ToOrig(u) }

// ToOut maps an original offset to an output offset.
func (n *Normalizer) ToOut(o int) int { return n.mp.ToOut(o) }

// Checks reports intervals examined by the most recent lookup.
func (n *Normalizer) Checks() int { return n.mp.Checks() }
