// Package norm is a streaming normalizer of line endings and trailing
// whitespace with a bidirectional offset map. Not safe for concurrent use.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type Ending uint8

const (
	Keep      Ending = iota // leave final newlines as produced
	EnsureOne               // nonempty output ends with exactly one '\n'
	TrimEmpty               // drop empty final lines; never adds a newline
)

// Config: zero WSMax/OutMax means unlimited.
type Config struct {
	End    Ending
	Strict bool
	WSMax  int
	OutMax int
}

var (
	ErrClosed    = errors.New("norm: writer closed")
	ErrNUL       = errors.New("norm: NUL byte")
	ErrWSPending = errors.New("norm: pending whitespace overflow")
	ErrOutput    = errors.New("norm: output limit exceeded")
)

// OffsetError attaches the original byte offset to a sentinel cause.
type OffsetError struct {
	Op     string
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

type Normalizer struct {
	cfg                                  Config
	out                                  []byte
	b                                    span.Builder
	holdCR                               bool
	ws                                   ws.Run
	consumed                             int
	closed                               bool
}

func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

func (n *Normalizer) fail(off int, err error) error {
	n.closed = true
	return &OffsetError{Op: "write", Offset: off, Err: err}
}

func (n *Normalizer) emit(c byte) { n.out = append(n.out, c); n.b.Copy(1) }
func (n *Normalizer) room(add int) bool {
	return n.cfg.OutMax == 0 || len(n.out)+add <= n.cfg.OutMax
}

func (n *Normalizer) flushWS() {
	for _, c := range n.ws.Bytes {
		n.emit(c)
	}
	n.ws.Reset()
}

func (n *Normalizer) dropWS() {
	if n.ws.Active() {
		n.b.Delete(n.ws.Len())
		n.ws.Reset()
	}
}

// resolveCR commits a held CR: with LF it is CRLF (CR deleted), else the lone
// CR is itself a line ending (its byte maps to the emitted '\n').
func (n *Normalizer) resolveCR(lf bool) error {
	n.dropWS()
	n.holdCR = false
	n.consumed++
	if lf {
		n.b.Delete(1)
		n.consumed++
	}
	if !n.room(1) {
		return n.fail(n.consumed, ErrOutput)
	}
	n.emit('\n')
	return nil
}

func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed {
		return 0, ErrClosed
	}
	start := n.consumed
	for j := 0; j < len(p); j++ {
		c, off := p[j], n.consumed
		var err error
		switch {
		case n.cfg.Strict && c == 0:
			err = ErrNUL
		case n.holdCR && c == eol.LF:
			err = n.resolveCR(true)
		case n.holdCR:
			err = n.resolveCR(false)
		}
		if err != nil {
			if err == ErrNUL {
				err = n.fail(off, ErrNUL)
			}
			return n.consumed - start, err
		}
		switch {
		case c == eol.CR:
			n.holdCR = true
		case c == eol.LF:
			n.consumed++
			n.dropWS()
			if !n.room(1) {
				return n.consumed - start, n.fail(off, ErrOutput)
			}
			n.emit('\n')
		case ws.IsSpace(c):
			if n.cfg.WSMax > 0 && n.ws.Len() >= n.cfg.WSMax {
				return n.consumed - start, n.fail(off, ErrWSPending)
			}
			n.ws.Add(c, off)
			n.consumed++
		default:
			n.flushWS()
			n.consumed++
			if !n.room(1) {
				return n.consumed - start, n.fail(off, ErrOutput)
			}
			n.emit(c)
		}
	}
	return n.consumed - start, nil
}

func (n *Normalizer) Close() error {
	if n.closed {
		return ErrClosed
	}
	n.closed = true
	if n.holdCR {
		if err := n.resolveCR(false); err != nil {
			return err
		}
	} else {
		n.flushWS()
	}
	return n.applyEnding()
}

func (n *Normalizer) applyEnding() error {
	k := 0
	if n.cfg.End != Keep {
		k = trailingLF(n.out)
	}
	if (n.cfg.End == EnsureOne || n.cfg.End == TrimEmpty) && k > 1 {
		n.b.Retract(k - 1)
		n.out = n.out[:len(n.out)-k+1]
		k = 1
	}
	if n.cfg.End == EnsureOne && n.consumed > 0 && k == 0 {
		if !n.room(1) {
			return n.fail(n.consumed, ErrOutput)
		}
		n.out = append(n.out, '\n')
		n.b.Insert(1, n.consumed)
	}
	return nil
}

func trailingLF(b []byte) int {
	k := 0
	for len(b)-k-1 >= 0 && b[len(b)-1-k] == '\n' {
		k++
	}
	return k
}

func (n *Normalizer) Output() []byte    { return n.out }
func (n *Normalizer) Map() *span.Map    { return n.b.Map() }
func (n *Normalizer) Consumed() int     { return n.consumed }
func (n *Normalizer) HeldCR() bool      { return n.holdCR }
func (n *Normalizer) PendingWS() []byte { return n.ws.Bytes }
