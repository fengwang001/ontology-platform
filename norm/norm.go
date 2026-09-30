// Package norm 是流式行尾与行尾空白规范化器。Normalizer 非并发安全，状态仅存内存。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type EndPolicy uint8

const Keep, Ensure, Trim EndPolicy = 0, 1, 2

var (
	ErrWhitespaceLimit = ws.ErrWhitespaceLimit
	ErrOutputLimit     = errors.New("norm: output byte limit exceeded")
	ErrNUL             = errors.New("norm: NUL byte in strict mode")
	ErrClosed          = errors.New("norm: write after terminal state")
)

type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

type Config struct {
	End                          EndPolicy
	WhitespaceLimit, OutputLimit int
	StrictNUL, Fragment          bool
}
type Normalizer struct {
	cfg  Config
	e    *eol.Tracker
	w    *ws.Tracker
	m    *span.Map
	out  []byte
	orig int
	done bool
	err  error
}

func New(c Config) *Normalizer {
	return &Normalizer{cfg: c, e: eol.New(), w: ws.New(0), m: &span.Map{}}
}
func (n *Normalizer) Map() *span.Map { return n.m }
func (n *Normalizer) Output() []byte { return n.out }
func (n *Normalizer) Err() error     { return n.err }
func (n *Normalizer) OrigLen() int   { return n.orig }
func (n *Normalizer) when(c bool, f func()) {
	if c {
		f()
	}
}
func (n *Normalizer) mark(e error) { n.done, n.err = true, &OffsetError{Err: e, Offset: n.orig} }
func (n *Normalizer) overOut(a int) bool {
	return n.cfg.OutputLimit > 0 && len(n.out)+a > n.cfg.OutputLimit
}
func (n *Normalizer) emit(p int, b []byte) {
	n.when(n.overOut(len(b)), func() { n.mark(ErrOutputLimit) })
	n.when(!n.done, func() { n.m.Copy(len(b)); n.out = append(n.out, b...) })
}
func (n *Normalizer) Write(p []byte) error {
	n.when(n.done, func() { n.mark(ErrClosed) })
	for i := 0; i < len(p) && n.err == nil; i++ {
		n.err = n.feed(p[i])
	}
	return n.err
}
func (n *Normalizer) feed(b byte) error {
	n.when(b == 0 && n.cfg.StrictNUL, func() { n.mark(ErrNUL) })
	pending, pos, ev := n.e.Pending(), n.orig, n.e.Feed(b)
	n.orig++
	switch ev {
	case eol.LF:
		d := n.w.Drop()
		n.when(pending, func() { d = 1 })
		n.m.Delete(d)
		n.emit(pos, nl)
		return n.err
	case eol.CR:
		n.m.Delete(n.w.Drop())
		n.emit(pos-1, nl)
		return n.err
	case eol.PendingCR:
		n.m.Delete(n.w.Drop())
		return nil
	}
	run, isW, werr := n.w.Feed(b, n.cfg.WhitespaceLimit)
	n.when(werr != nil, func() { n.mark(werr) })
	n.emit(pos, run)
	n.when(!isW && n.err == nil, func() { n.emit(pos, []byte{b}) })
	return n.err
}
func (n *Normalizer) Close() error {
	closed := n.done
	n.done = true
	switch {
	case closed:
	case n.e.Flush() == eol.CR:
		n.emit(n.orig-1, nl)
	case n.cfg.Fragment:
		if run := n.w.Flush(); len(run) > 0 {
			n.emit(n.orig, run)
		}
	default:
		n.m.Delete(n.w.Drop())
	}
	n.when(n.err != nil, func() { n.done = false })
	n.when(n.err == nil && !n.cfg.Fragment && !closed, func() { ApplyEndPolicy(n.m, &n.out, n.cfg.End, n.orig > 0) })
	n.when(n.err == nil && !n.cfg.Fragment && n.overOut(0), func() {
		n.err = &OffsetError{Err: ErrOutputLimit, Offset: n.orig}
	})
	return n.err
}
func Process(p []byte, c Config) ([]byte, *span.Map, error) {
	n := New(c)
	err := n.Write(p)
	if err == nil {
		err = n.Close()
	}
	return n.Output(), n.Map(), err
}

var nl = []byte{'\n'}

func tailNL(b []byte) int {
	k := 0
	for k < len(b) && b[len(b)-1-k] == '\n' {
		k++
	}
	return k
}

func ApplyEndPolicy(m *span.Map, out *[]byte, p EndPolicy, nonEmpty bool) {
	if !nonEmpty || p == Keep {
		return
	}
	switch k := tailNL(*out); {
	case k > 1:
		cut := len(*out) - k + 1
		m.DeleteTailOutput(cut)
		*out = (*out)[:cut]
	case k == 0:
		m.Insert(nl)
		*out = append(*out, '\n')
	}
}
