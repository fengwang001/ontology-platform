// Package norm is a streaming normalizer for mixed line endings and trailing
// whitespace with an exact bidirectional offset map.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Trailing selects the end-of-file newline policy.
type Trailing int

const (
	// Keep preserves the original trailing newline structure.
	Keep Trailing = iota
	// EnsureOne guarantees exactly one trailing newline on non-empty content.
	EnsureOne
	// TrimBlank removes trailing blank lines, keeping one after a real last line.
	TrimBlank
)

var (
	// ErrWhitespaceLimit: the deferred-whitespace buffer exceeded its limit.
	ErrWhitespaceLimit = errors.New("norm: trailing whitespace buffer limit exceeded")
	// ErrOutputLimit: output exceeded its byte limit.
	ErrOutputLimit = errors.New("norm: output byte limit exceeded")
	// ErrNUL: a NUL byte was seen in strict mode.
	ErrNUL = errors.New("norm: NUL byte in strict mode")
	// ErrClosed: Write was called after the terminal state.
	ErrClosed = errors.New("norm: write after terminal state")
)

// OffsetError attaches the original-stream byte offset to a sentinel error.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Option configures a Normalizer.
type Option func(*Normalizer)

// WithTrailing sets the end-of-file policy (default Keep).
func WithTrailing(t Trailing) Option { return func(n *Normalizer) { n.trail = t } }

// WithStrictNUL turns NUL bytes into errors instead of passing them through.
func WithStrictNUL() Option { return func(n *Normalizer) { n.strictNUL = true } }

// WithWhitespaceLimit caps buffered unresolved whitespace (0 = unlimited).
func WithWhitespaceLimit(l int) Option { return func(n *Normalizer) { n.wsLimit = l } }

// WithOutputLimit caps total output bytes (0 = unlimited).
func WithOutputLimit(l int) Option { return func(n *Normalizer) { n.outLimit = l } }

// Normalizer is a single-threaded streaming normalizer. A single instance is
// NOT safe for concurrent use; use separate instances for concurrency.
type Normalizer struct {
	trail     Trailing
	strictNUL bool
	wsLimit   int
	outLimit  int

	sc    eol.Scanner
	wb    ws.Buf
	out   []byte
	mapb  span.Builder
	done  bool
	fail  error
	insO  int // inserted newline: original anchor (-1 = none)
	insP  int // inserted newline: output position
}

// New creates a Normalizer.
func New(opts ...Option) *Normalizer {
	n := &Normalizer{insO: -1, insP: -1}
	for _, o := range opts {
		o(n)
	}
	n.wb = *ws.NewBuf(n.wsLimit)
	return n
}

func (n *Normalizer) failAt(err error, off int) error {
	e := &OffsetError{Err: err, Offset: off}
	n.fail = e
	return e
}

func (n *Normalizer) emitByte(c byte) error {
	if n.outLimit > 0 && len(n.out) >= n.outLimit {
		return n.failAt(ErrOutputLimit, n.mapb.OrigLen())
	}
	n.out = append(n.out, c)
	n.mapb.Retain(1)
	return nil
}

func (n *Normalizer) flushWS() {
	k := n.wb.Len()
	n.out = n.wb.Flush(n.out)
	n.mapb.Retain(k)
}

func (n *Normalizer) emitNewline(origLen int) error {
	n.mapb.Drop(n.wb.Len())
	n.wb.Drop()
	if origLen == 2 {
		n.mapb.Drop(1) // the \r of \r\n (or lone \r pending handling)
	}
	if n.outLimit > 0 && len(n.out) >= n.outLimit {
		return n.failAt(ErrOutputLimit, n.mapb.OrigLen())
	}
	n.out = append(n.out, '\n')
	n.mapb.Retain(1)
	return nil
}

// feed pushes p. With lookahead the last byte of p is never consumed (used by
// par); final resolves a pending \r as a lone ending and implies no lookahead.
func (n *Normalizer) feed(p []byte, final, lookahead bool) error {
	if n.done || n.fail != nil {
		return n.fail
	}
	consume := len(p)
	if lookahead && !final && len(p) > 0 {
		consume--
	}
	proc := func(ev eol.Event, v byte, origLen int) {
		switch ev {
		case eol.Newline:
			if n.fail == nil {
				n.fail = n.emitNewline(origLen)
			}
		default:
			if v == 0 && n.strictNUL {
				n.fail = n.failAt(ErrNUL, n.mapb.OrigLen()+n.wb.Len())
				return
			}
			if ws.IsSpace(v) {
				if !n.wb.Add(v) {
					n.fail = n.failAt(ErrWhitespaceLimit, n.mapb.OrigLen()+n.wb.Len())
					return
				}
				return
			}
			n.flushWS()
			if n.fail == nil {
				n.fail = n.emitByte(v)
			}
		}
	}
	if consume > 0 {
		n.sc.Feed(p[:consume], proc)
	}
	if final {
		n.sc.Finish(proc)
	}
	return n.fail
}

// Write appends bytes to the stream.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.done || n.fail != nil {
		return 0, &OffsetError{Err: ErrClosed, Offset: n.mapb.OrigLen()}
	}
	if err := n.feed(p, false, false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close finalizes the stream and applies the trailing-newline policy. After
// Close the instance is in a terminal state; further Writes return ErrClosed.
func (n *Normalizer) Close() error {
	if n.done {
		return n.fail
	}
	n.done = true
	if n.fail != nil {
		return n.fail
	}
	if err := n.feed(nil, true, false); err != nil {
		return err
	}
	// Trailing residual: its whitespace is trailing too.
	n.mapb.Drop(n.wb.Len())
	n.wb.Drop()
	switch n.trail {
	case EnsureOne:
		if n.mapb.OrigLen() > 0 && (len(n.out) == 0 || n.out[len(n.out)-1] != '\n') {
			n.insO, n.insP = n.mapb.OrigLen(), len(n.out)
			n.out = append(n.out, '\n')
		}
	case TrimBlank:
		if len(n.out) > 0 {
			j := len(n.out)
			for j > 0 && n.out[j-1] == '\n' {
				j--
			}
			blanks := len(n.out) - j
			if blanks == len(n.out) {
				rem := n.mapb.Retract(blanks)
				n.out = n.out[:0]
				if rem > 0 {
					n.insO, n.insP = 0, 0
					n.out = append(n.out, '\n')
				}
			} else if blanks > 1 {
				n.mapb.Retract(blanks - 1)
				n.out = n.out[:j+1]
			}
		}
	}
	return nil
}

// Output returns the normalized bytes (available after Close).
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the bidirectional mapper (available after Close).
func (n *Normalizer) Map() *span.Mapper { return n.mapb.Mapper(n.insO, n.insP) }

// unresolved is the trailing suffix length still undecided after a non-final
// feed: a pending \r plus buffered whitespace.
func (n *Normalizer) unresolved() int {
	u := n.wb.Len()
	if n.sc.Pending() {
		u++
	}
	return u
}
