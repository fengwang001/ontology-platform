// Package norm streams EOL/trailing-whitespace normalization with a
// bidirectional offset map. One Normalizer is single-goroutine use.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type Policy int

const (
	Keep Policy = iota
	EnsureOne
	Trim
)

type Config struct {
	End           Policy
	StrictNUL     bool
	WSBufferLimit int
	MaxOutput     int
}

var (
	ErrNUL       = errors.New("strict mode: NUL byte")
	ErrWSBuffer  = errors.New("undecided whitespace buffer limit exceeded")
	ErrMaxOutput = errors.New("output size limit exceeded")
	ErrClosed    = errors.New("normalizer is in a terminal state")
)

type OpError struct {
	Op     string
	Offset int // original byte offset
	Err    error
}

func (e *OpError) Error() string { return e.Err.Error() }
func (e *OpError) Unwrap() error { return e.Err }

type Normalizer struct {
	cfg            Config
	out            []byte
	b              span.Builder
	eo             *eol.State
	orig, opos     int
	wsBuf          []byte
	wsOrig, crOrig int
	terminal       error
	closed         bool
}

func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, eo: eol.New()}
}

func (n *Normalizer) fail(op string, off int, err error) error {
	n.terminal = &OpError{Op: op, Offset: off, Err: err}
	return n.terminal
}

func (n *Normalizer) emit(orig int, p []byte) error {
	if n.cfg.MaxOutput > 0 && n.opos+len(p) > n.cfg.MaxOutput {
		return n.fail("output", orig, ErrMaxOutput)
	}
	n.b.Identity(orig, n.opos, len(p))
	n.out = append(n.out, p...)
	n.opos += len(p)
	return nil
}

func (n *Normalizer) dropWS() {
	if l := len(n.wsBuf); l > 0 {
		n.b.Deleted(n.wsOrig, n.opos, l)
		n.wsBuf, n.wsOrig = n.wsBuf[:0], 0
	}
}

func (n *Normalizer) keepWS() error {
	if len(n.wsBuf) == 0 {
		return nil
	}
	orig, buf := n.wsOrig, append([]byte(nil), n.wsBuf...)
	n.wsBuf, n.wsOrig = n.wsBuf[:0], 0
	return n.emit(orig, buf)
}

func (n *Normalizer) Write(p []byte) (int, error) {
	if n.terminal != nil {
		return 0, n.terminal
	}
	if n.closed {
		return 0, n.fail("closed", n.orig, ErrClosed)
	}
	for k, c := range p {
		if c == 0 && n.cfg.StrictNUL {
			return k, n.fail("nul", n.orig, ErrNUL)
		}
		off, lone, kind := n.orig, false, eol.KindNone
		if c != 0 || !n.cfg.StrictNUL {
			lone, kind = n.eo.Feed(c)
		}
		if lone {
			n.dropWS()
			if err := n.emit(n.crOrig, []byte{'\n'}); err != nil {
				return k, err
			}
		}
		switch {
		case kind == eol.KindLF, kind == eol.KindCR:
			n.dropWS()
			if kind == eol.KindCR {
				n.crOrig = off
			} else if err := n.emit(off, []byte{'\n'}); err != nil {
				return k, err
			}
		case n.eo.Pending():
			n.dropWS()
			n.crOrig = off
		case ws.IsSpace(c):
			if len(n.wsBuf) == 0 {
				n.wsOrig = off
			}
			if lim := n.cfg.WSBufferLimit; lim > 0 && len(n.wsBuf) >= lim {
				return k, n.fail("wsbuffer", off, ErrWSBuffer)
			}
			n.wsBuf = append(n.wsBuf, c)
		default:
			if err := n.keepWS(); err != nil {
				return k, err
			}
			if err := n.emit(off, []byte{c}); err != nil {
				return k, err
			}
		}
		n.orig++
	}
	return len(p), nil
}

func (n *Normalizer) Close() error {
	if n.terminal != nil {
		return n.terminal
	}
	if n.closed {
		return n.fail("closed", n.orig, ErrClosed)
	}
	n.closed = true
	if n.eo.Flush() {
		n.dropWS()
		if err := n.emit(n.crOrig, []byte{'\n'}); err != nil {
			return err
		}
	}
	n.dropWS()
	if n.cfg.End == Trim {
		es := n.b.Entries()
		for len(n.out) > 1 && n.out[len(n.out)-1] == '\n' {
			n.out = n.out[:len(n.out)-1]
			n.opos--
			es = span.PopLastIdentity(es, n.opos)
		}
		n.b = *span.From(es)
	}
	if n.cfg.End != Keep && len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
		if err := n.emit(n.orig, []byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

func (n *Normalizer) Output() []byte { return n.out }

func (n *Normalizer) Map() *span.Map {
	return span.NewMap(n.b.Entries(), n.orig, n.opos)
}
