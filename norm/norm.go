// Package norm is a streaming normalizer of line endings and
// trailing whitespace with a two-way byte-offset map.
//
// Rules: \r\n and lone \r become \n; spaces/tabs at the end of a
// line (or stream) are dropped; lines containing only whitespace
// survive as empty lines. NUL bytes and invalid UTF-8 pass through
// unless StrictNUL is set. A Normalizer is not safe for concurrent
// use; give every goroutine its own instance.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Ending selects the final-newline policy.
type Ending int

const (
	Keep   Ending = iota // leave the ending exactly as supplied
	Ensure               // non-empty output ends with exactly one \n
	Trim                 // drop trailing blank lines, keep one \n
)

// Config configures a Normalizer. Zero/negative limits are unlimited.
type Config struct {
	Ending    Ending
	MaxWS     int64 // buffered trailing-whitespace cap
	MaxOutput int64 // total output cap
	StrictNUL bool
}

var (
	ErrClosed   = errors.New("norm: write after terminal state")
	ErrWSLimit  = errors.New("norm: trailing-whitespace buffer limit exceeded")
	ErrOutLimit = errors.New("norm: output limit exceeded")
)

// Error carries a distinguishable cause and the original offset at
// which the failing input byte was observed.
type Error struct {
	Err    error
	Offset int64
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Normalizer streams Write calls into one normalized buffer.
type Normalizer struct {
	cfg      Config
	eol      *eol.Decoder
	tab      *ws.Tracker
	mp       *span.Map
	out      []byte
	consumed int64
	ended    bool
}

// New returns a Normalizer.
func New(cfg Config) *Normalizer {
	hint := int(cfg.MaxWS)
	if hint <= 0 || hint > 1<<16 {
		hint = 1 << 10
	}
	return &Normalizer{cfg: cfg, eol: eol.New(), tab: ws.New(hint), mp: span.New()}
}

// Normalize normalizes a whole buffer in one pass.
func Normalize(p []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(p); err != nil {
		return n.Output(), n.Map(), err
	}
	if err := n.Close(); err != nil {
		return n.Output(), n.Map(), err
	}
	return n.Output(), n.Map(), nil
}

// Write feeds one chunk.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.ended {
		return 0, &Error{Err: ErrClosed, Offset: n.consumed}
	}
	for i, b := range p {
		if err := n.feed(b, n.consumed+int64(i)); err != nil {
			n.consumed += int64(i)
			n.ended = true
			return i, err
		}
	}
	n.consumed += int64(len(p))
	return len(p), nil
}

func (n *Normalizer) fail(err error, off int64) error {
	return &Error{Err: err, Offset: off}
}

func (n *Normalizer) feed(b byte, off int64) error {
	if b == 0 && n.cfg.StrictNUL {
		return n.fail(errors.New("norm: NUL byte in strict mode"), off)
	}
	r := n.eol.Feed(b)
	if r.EmitNL != eol.NLNone {
		n.newline(r.EmitNL)
	}
	switch {
	case r.Hold:
		return nil
	case r.Pass:
		if ws.IsSpace(b) {
			if n.cfg.MaxWS > 0 && int64(n.tab.Len()) >= n.cfg.MaxWS {
				return n.fail(ErrWSLimit, off)
			}
			n.tab.Add(b)
			return nil
		}
		if n.tab.Len() > 0 {
			run := n.tab.Drain()
			n.out = append(n.out, run...)
			n.mp.Survive(int64(len(run)))
		}
		if err := n.ensureCap(1, off); err != nil {
			return err
		}
		n.out = append(n.out, b)
		n.mp.Survive(1)
	}
	return nil
}

func (n *Normalizer) newline(src eol.NLSrc) {
	if d := n.tab.Drop(); d > 0 {
		n.mp.Delete(int64(d))
	}
	if src == eol.NLCRLF {
		n.mp.Delete(1)
	}
	n.out = append(n.out, '\n')
	if src == eol.NLCR {
		n.mp.Append(1)
	} else {
		n.mp.Survive(1)
	}
}

func (n *Normalizer) ensureCap(add int, off int64) error {
	if n.cfg.MaxOutput > 0 && int64(len(n.out)+add) > n.cfg.MaxOutput {
		return &Error{Err: ErrOutLimit, Offset: off}
	}
	return nil
}

// Close applies the final-newline policy and enters the terminal state.
func (n *Normalizer) Close() error {
	if n.ended {
		return &Error{Err: ErrClosed, Offset: n.consumed}
	}
	if n.eol.Close() {
		n.newline(eol.NLCR)
	}
	if d := n.tab.Drop(); d > 0 {
		n.mp.Delete(int64(d))
	}
	n.finish()
	n.ended = true
	return nil
}

func (n *Normalizer) finish() {
	switch n.cfg.Ending {
	case Ensure:
		if len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
			n.out = append(n.out, '\n')
			n.mp.Append(1)
		}
	}
	if n.cfg.Ending == Ensure {
		return
	}
	if n.cfg.Ending != Trim || len(n.out) == 0 {
		return
	}
	i := len(n.out)
	for i > 0 && n.out[i-1] == '\n' {
		i--
	}
	if i == 0 {
		n.out = n.out[:1]
		n.mp.TruncateOut(1)
		return
	}
	if i < len(n.out)-1 {
		n.out = n.out[:i+1]
		n.mp.TruncateOut(int64(i + 1))
	}
}

// Output returns the normalized bytes.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the offset map.
func (n *Normalizer) Map() *span.Map { return n.mp }
