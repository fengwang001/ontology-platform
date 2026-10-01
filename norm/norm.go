// Package norm implements streaming line-ending and whitespace normalization.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type (
	Policy int      // Preserve: keep as found; EnsureOne: exactly one if non-empty; Collapse: drop trailing blank lines
	Error  struct { // decidable failure: Err is a sentinel below, Orig the input offset
		Err  error
		Orig int
	}
	Config struct { // non-positive limits mean unlimited
		Policy   Policy
		Strict   bool // reject NUL bytes with ErrNUL
		WSLimit  int  // max pending whitespace bytes
		OutLimit int  // max output bytes
	}
	Norm struct { // streaming normalizer; not concurrency-safe; Output/Map valid after Close
		cfg          Config
		sc           eol.Scanner
		wsb          ws.Buffer
		m            span.Map
		out          []byte
		orig         int
		held, second int // pending trailing newlines; orig offset of the 2nd one
		term         bool
		evErr        error
		pend         []pend
	}
	pend struct{ orig, del, nl int } // deletion inside the held run, committed at flush
)

const Preserve, EnsureOne, Collapse Policy = 0, 1, 2

var ErrNUL = errors.New("norm: NUL byte in strict mode")
var ErrWSLimit = errors.New("norm: pending whitespace over limit")
var ErrOutLimit = errors.New("norm: output over limit")
var ErrClosed = errors.New("norm: instance is in terminal state")

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
func New(cfg Config) *Norm     { n := &Norm{cfg: cfg}; n.wsb.Limit = cfg.WSLimit; return n }
func (n *Norm) Write(p []byte) (int, error) {
	if n.term {
		return 0, &Error{ErrClosed, n.orig}
	}
	for j, b := range p {
		n.sc.Feed(b, n.onEv)
		n.orig++
		if n.evErr != nil {
			n.term = true
			return j, n.evErr
		}
	}
	return len(p), nil
}
func (n *Norm) onEv(ev eol.Ev) {
	if n.evErr != nil {
		return
	}
	if ev.Kind == eol.Newline {
		n.delete(n.wsb.Start(), n.wsb.Len())
		n.wsb.Drop()
		if ev.Drop > 0 {
			n.delete(ev.Pos, ev.Drop)
		}
		if n.held == 1 {
			n.second = ev.Pos + ev.Drop
		}
		n.held++
		return
	}
	if n.cfg.Strict && ev.B == 0 {
		n.evErr = &Error{ErrNUL, ev.Pos}
		return
	}
	if ev.B == ' ' || ev.B == '\t' {
		if n.wsb.Add(ev.B, ev.Pos) != nil {
			n.evErr = &Error{ErrWSLimit, ev.Pos}
		}
		return
	}
	n.flush()
	n.put(ev.B, ev.Pos)
}
func (n *Norm) delete(orig, del int) {
	if n.held > 0 {
		n.pend = append(n.pend, pend{orig, del, n.held})
		return
	}
	n.m.AddDelete(orig, del, len(n.out))
}
func (n *Norm) commit(maxNL int) {
	for _, p := range n.pend {
		if p.nl <= maxNL {
			n.m.AddDelete(p.orig, p.del, len(n.out)+p.nl)
		}
	}
	n.pend = nil
}
func (n *Norm) flush() {
	n.commit(1 << 30)
	for k := 0; k < n.held; k++ {
		n.put('\n', n.orig)
	}
	n.held = 0
	buf, start := n.wsb.Flush()
	for k, c := range buf {
		n.put(c, start+k)
	}
}
func (n *Norm) put(c byte, pos int) {
	if n.evErr == nil && n.cfg.OutLimit > 0 && len(n.out) >= n.cfg.OutLimit {
		n.evErr = &Error{ErrOutLimit, pos}
	}
	if n.evErr == nil {
		n.out = append(n.out, c)
	}
}
func (n *Norm) Close() error {
	if n.term {
		return &Error{ErrClosed, n.orig}
	}
	n.term = true
	n.sc.End(n.onEv)
	n.delete(n.wsb.Start(), n.wsb.Len())
	n.wsb.Drop()
	appendNL := n.cfg.Policy == EnsureOne && n.held == 0
	if n.cfg.Policy != Preserve && n.held > 1 {
		n.commit(1)
		n.m.AddDelete(n.second, n.orig-n.second, len(n.out)+1)
		n.held = 1
	}
	n.flush()
	if appendNL && len(n.out) > 0 {
		n.put('\n', n.orig)
		n.m.Appended++
	}
	n.m.Orig, n.m.Out = n.orig, len(n.out)
	return n.evErr
}
func (n *Norm) Output() []byte { return n.out }
func (n *Norm) Map() *span.Map { return &n.m }
