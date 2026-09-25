// Package norm is a streaming line-ending and whitespace normalizer with
// bidirectional offset mapping. A single Norm is not safe for concurrent use.
package norm

import (
	"bytes"
	"fmt"
	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type Policy int // final-newline strategy
type Kind int   // classifies a *Error so failures are decidable

type Error struct { // decidable normalization failure with original offset
	Kind Kind
	Off  int
}

type Options struct { // MaxWS/MaxOut of 0 mean unlimited
	Policy              Policy
	Strict              bool
	MaxWS, MaxOut, Base int // Base: original offset of first byte (used by par)
}

type Norm struct {
	opt  Options
	off  int
	out  []byte
	b    span.Builder
	cr   eol.Pend
	wsb  *ws.Buf
	err  *Error // latched terminal error
	done bool
}

const (
	Keep      Policy = iota // leave the end of the output as is
	EnsureOne               // output ends with exactly one '\n'
	Strip                   // drop trailing blank lines; keep one '\n' if non-empty
)
const (
	KindNUL    Kind = iota // NUL byte in strict mode
	KindWS                 // whitespace buffer limit exceeded
	KindOut                // output byte limit exceeded
	KindClosed             // write after terminal state
)

func (e *Error) Error() string {
	return fmt.Sprintf("norm: error kind %d at original offset %d", int(e.Kind), e.Off)
}
func New(o Options) *Norm { return &Norm{opt: o, off: o.Base, wsb: ws.New(o.MaxWS)} }
func (n *Norm) Write(p []byte) (int, error) {
	if n.err != nil {
		return 0, n.err
	}
	if n.done {
		return 0, &Error{Kind: KindClosed, Off: n.off}
	}
	for k, c := range p {
		if err := n.step(c); err != nil {
			n.err = err
			return k, err
		}
	}
	return len(p), nil
}
func (n *Norm) step(c byte) *Error {
	i := n.off
	n.off++
	if n.cr.On && n.cr.Feed(c) { // lone CR: retained as '\n'
		if err := n.emit('\n', n.cr.Off); err != nil {
			return err
		}
	}
	if c == 0 && n.opt.Strict {
		return &Error{Kind: KindNUL, Off: i}
	}
	switch eol.Kind(c) {
	case eol.CR:
		n.wsb.Reset() // whitespace before an EOL was trailing: deleted
		n.cr.Set(i)
	case eol.LF:
		n.wsb.Reset()
		return n.emit('\n', i)
	default:
		if ws.IsWS(c) {
			if !n.wsb.Add(c, i) {
				return &Error{Kind: KindWS, Off: i}
			}
			return nil
		}
		for k, b := range n.wsb.Bytes() { // content: ws was not trailing
			if err := n.emit(b, n.wsb.Start()+k); err != nil {
				return err
			}
		}
		n.wsb.Reset()
		return n.emit(c, i)
	}
	return nil
}
func (n *Norm) emit(c byte, orig int) *Error {
	if n.opt.MaxOut > 0 && len(n.out) >= n.opt.MaxOut {
		return &Error{Kind: KindOut, Off: orig}
	}
	n.b.Keep(orig, orig+1, len(n.out))
	n.out = append(n.out, c)
	return nil
}
func (n *Norm) Close() error {
	if n.done || n.err != nil {
		return n.err
	}
	if n.cr.On {
		n.cr.EOF()
		if err := n.emit('\n', n.cr.Off); err != nil {
			n.err = err
			return err
		}
	}
	n.wsb.Reset() // whitespace at end of stream is trailing: deleted
	n.out = Finalize(n.out, &n.b, n.opt.Policy)
	if n.opt.MaxOut > 0 && len(n.out) > n.opt.MaxOut {
		n.err = &Error{Kind: KindOut, Off: n.off}
		return n.err
	}
	n.done = true
	return nil
}

func Finalize(out []byte, b *span.Builder, p Policy) []byte {
	if p != Keep {
		out = bytes.TrimRight(out, "\n")
		b.TruncateOut(len(out))
	}
	if p == EnsureOne || p == Strip && len(out) > 0 {
		out = append(out, '\n')
		b.Synth(1)
	}
	return out
}
func (n *Norm) Output() []byte { return n.out }
func (n *Norm) Map() *span.Map { return n.b.Build(n.off) }

// Pending reports the undecided tail (pending CR or whitespace run, never both); used by par before Close.
func (n *Norm) Pending() (cr bool, crOff, wsStart, wsLen int) {
	return n.cr.On, n.cr.Off, n.wsb.Start(), n.wsb.Len()
}
