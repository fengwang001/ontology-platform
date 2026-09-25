// Package norm is a streaming line-ending/whitespace normalizer with a
// bidirectional offset map. Depends on eol, ws and span.
package norm

import (
	"errors"
	"fmt"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type Policy int

const (
	Keep Policy = iota
	EnsureOne
	TrimBlank
)

type Config struct {
	Policy                    Policy
	StrictNUL                 bool
	PendingLimit, OutputLimit int
}

var ErrNUL = errors.New("norm: NUL byte in strict mode")
var ErrPendingLimit = errors.New("norm: pending whitespace limit exceeded")
var ErrOutputLimit = errors.New("norm: output limit exceeded")
var ErrClosed = errors.New("norm: write after terminal state")

type OffsetError struct {
	Err error
	Off int
}

func (e *OffsetError) Error() string { return fmt.Sprintf("%v at offset %d", e.Err, e.Off) }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer is one stream; it is not safe for concurrent use.
type Normalizer struct {
	cfg   Config
	dec   *eol.Decider
	pend  *ws.Buf
	mp    span.Map
	out   []byte
	inoff int
	ended bool
	err   error
}

func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, dec: eol.New(), pend: ws.New(64)}
}

func (n *Normalizer) emit(b byte, off int) error {
	if lim := n.cfg.OutputLimit; lim > 0 && len(n.out) >= lim {
		return n.fatal(ErrOutputLimit, off)
	}
	n.out = append(n.out, b)
	n.mp.Keep(off, 1)
	return nil
}
func (n *Normalizer) fatal(err error, off int) error {
	n.ended, n.err = true, &OffsetError{Err: err, Off: off}
	return n.err
}
func (n *Normalizer) dropPending() {
	n.mp.Delete(n.pend.FirstOff(), n.pend.Len(), len(n.out))
	n.pend.Reset()
}
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.ended {
		return 0, n.afterClose()
	}
	for k, b := range p {
		if err := n.step(b, n.inoff+k); err != nil {
			return k, err
		}
	}
	n.inoff += len(p)
	return len(p), nil
}
func (n *Normalizer) step(b byte, off int) error {
	if b == 0 && n.cfg.StrictNUL {
		return n.fatal(ErrNUL, off)
	}
	nlAt, text := n.dec.Feed(b, off)
	if nlAt >= 0 {
		n.dropPending()
		if err := n.emit('\n', nlAt); err != nil {
			return err
		}
	}
	if !text {
		return nil
	}
	if ws.IsSpace(b) {
		if lim := n.cfg.PendingLimit; lim > 0 && n.pend.Len() >= lim {
			return n.fatal(ErrPendingLimit, off)
		}
		n.pend.Add(b, off)
		return nil
	}
	po := n.pend.Offsets()
	for i, w := range n.pend.Bytes() {
		if err := n.emit(w, po[i]); err != nil {
			return err
		}
	}
	n.pend.Reset()
	return n.emit(b, off)
}

// Close finalizes the stream and applies the final-newline policy.
func (n *Normalizer) Close() error {
	if n.ended {
		return n.afterClose()
	}
	n.ended = true
	if at, ok := n.dec.Flush(); ok {
		n.dropPending()
		if err := n.emit('\n', at); err != nil {
			return err
		}
	}
	n.dropPending()
	switch n.cfg.Policy {
	case EnsureOne:
		if len(n.out) == 0 || n.out[len(n.out)-1] != '\n' {
			if lim := n.cfg.OutputLimit; lim > 0 && len(n.out) >= lim {
				return n.fatal(ErrOutputLimit, n.inoff)
			}
			n.out = append(n.out, '\n')
			n.mp.Synth(0)
		}
	case TrimBlank:
		k := len(n.out)
		for k > 0 && n.out[k-1] == '\n' {
			k--
		}
		if k < len(n.out) {
			n.out = append(n.out[:k], '\n')
			n.mp.TrimTo(k, 0)
		}
	}
	return nil
}

func (n *Normalizer) afterClose() error {
	if n.err == nil {
		n.err = ErrClosed
	}
	return n.err
}
func (n *Normalizer) Output() []byte { return n.out }
func (n *Normalizer) M() *span.Map   { return &n.mp }
func (n *Normalizer) Err() error     { return n.err }
