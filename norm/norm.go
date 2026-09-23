// Package norm is a streaming normalizer of line endings and trailing
// whitespace with a bidirectional byte-offset map. A Normalizer is not safe
// for concurrent use.
package norm

import (
	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Options configures a Normalizer.
type Options struct {
	Policy    Policy
	StrictNUL bool  // fail on the first NUL byte
	WSBuffer  int   // undecided run limit; <= 0 means ws.DefaultLimit
	MaxOutput int64 // output byte limit; <= 0 means unlimited
	openEnd   bool  // emit dangling \r / trailing ws as-is (used by par)
}

// OpenOption enables open-end mode (used by par chunks).
func OpenOption(o Options) Options { o.openEnd = true; return o }

// Normalizer is the streaming normalizer.
type Normalizer struct {
	opt     Options
	dec     eol.Decoder
	tr      *ws.Tracker
	sb      *span.Builder
	out     []byte
	buf     []byte
	wsStart int64
	off     int64
	closed  bool
	err     error
	final   *span.Map
}

// New creates a Normalizer.
func New(opt Options) *Normalizer {
	return &Normalizer{opt: opt, tr: ws.New(opt.WSBuffer), sb: span.NewBuilder()}
}

func (n *Normalizer) done() bool         { return n.closed || n.err != nil }
func (n *Normalizer) fail(e error) error { n.err = e; return e }
func (n *Normalizer) Err() error         { return n.err }

func (n *Normalizer) room(add int64) error {
	if n.opt.MaxOutput > 0 && int64(len(n.out))+add > n.opt.MaxOutput {
		return n.fail(&OutputError{Limit: n.opt.MaxOutput})
	}
	return nil
}

// Close resolves dangling input and applies the configured end policy.
func (n *Normalizer) Close() error {
	if n.done() {
		return n.err
	}
	n.closed = true
	if ev, ok := n.dec.Flush(); ok {
		if err := n.line(n.off-1, ev.N, ev.B); err != nil {
			return err
		}
	}
	if err := n.flushWS(!n.opt.openEnd); err != nil {
		return err
	}
	out, m2, err := ApplyPolicy(n.out, n.sb.Build(), n.opt.Policy, n.opt.MaxOutput)
	if err != nil {
		return n.fail(err)
	}
	n.out, n.final = out, m2
	return nil
}

// Output returns the bytes produced so far.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the bidirectional map at the current position.
func (n *Normalizer) Map() *span.Map {
	if n.final != nil {
		return n.final
	}
	return n.sb.Build()
}

// Normalize is the one-shot convenience wrapper.
func Normalize(in []byte, opt Options) ([]byte, *span.Map, error) {
	n := New(opt)
	if _, err := n.Write(in); err != nil {
		return n.Output(), n.Map(), err
	}
	if err := n.Close(); err != nil {
		return n.Output(), n.Map(), err
	}
	return n.Output(), n.Map(), nil
}
