// Package norm is a streaming normalizer of mixed line endings and trailing
// whitespace with a bidirectional offset map. A single Normalizer is not
// safe for concurrent use; use separate instances per goroutine (see par).
package norm

import (
	"errors"

	"ontology/span"
	"ontology/ws"
)

// Final selects the end-of-stream newline policy.
type Final uint8

const (
	Keep Final = iota // leave the tail untouched
	One               // empty -> empty; otherwise exactly one trailing '\n'
	Trim              // drop trailing blank lines, keep one '\n'; all-blank -> empty
)

// Distinguishable sentinel errors; an *Error wraps them with an offset.
var (
	ErrNUL         = errors.New("norm: NUL byte in strict mode")
	ErrSpaceLimit  = errors.New("norm: trailing-whitespace buffer limit exceeded")
	ErrOutputLimit = errors.New("norm: output length limit exceeded")
	ErrTerminal    = errors.New("norm: write after terminal state")
)

// Error is a sentinel error carrying the original byte offset.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

// Config configures a Normalizer. Zero/negative limits mean unlimited.
type Config struct {
	Final       Final
	StrictNUL   bool
	SpaceLimit  int
	OutputLimit int
}

// Normalizer is the streaming state machine.
type Normalizer struct {
	cfg      Config
	out      []byte
	sb       span.Builder
	pend     ws.Pending
	off      int  // next original offset
	cr       bool // a '\r' seen, awaiting resolution
	terminal bool
}

// New returns a ready Normalizer.
func New(cfg Config) *Normalizer {
	n := &Normalizer{cfg: cfg}
	n.pend = *ws.New(cfg.SpaceLimit)
	return n
}

func (n *Normalizer) fail(k error, off int) error {
	n.terminal = true
	return &Error{Kind: k, Offset: off}
}

func (n *Normalizer) emit(b byte, off int) error {
	if n.cfg.OutputLimit > 0 && len(n.out) >= n.cfg.OutputLimit {
		return n.fail(ErrOutputLimit, off)
	}
	n.out = append(n.out, b)
	return nil
}

// flush decides the buffered whitespace: trailing -> deleted, interior -> kept.
func (n *Normalizer) flush(trailing bool) error {
	l := n.pend.Len()
	if l == 0 {
		return nil
	}
	if trailing {
		n.sb.Delete(l)
		n.pend.Drop()
		return nil
	}
	if n.cfg.OutputLimit > 0 && len(n.out)+l > n.cfg.OutputLimit {
		return n.fail(ErrOutputLimit, n.pend.Start())
	}
	n.out = append(n.out, n.pend.Bytes()...)
	n.sb.Direct(l)
	n.pend.Drop()
	return nil
}

func (n *Normalizer) writeByte(b byte) error {
	off := n.off
	if n.terminal {
		return n.fail(ErrTerminal, off)
	}
	if n.cfg.StrictNUL && b == 0 {
		return n.fail(ErrNUL, off)
	}
	if n.cr {
		if b != '\n' { // lone CR: its normalized '\n' has no source byte
			if err := n.emit('\n', off); err != nil {
				return err
			}
			n.sb.Insert(off)
		}
		n.cr = false
	}
	if ws.IsSpace(b) {
		if n.pend.Len() == 0 {
			n.pend.Reset(off)
		}
		if !n.pend.Add(b) {
			return n.fail(ErrSpaceLimit, off)
		}
		n.off++
		return nil
	}
	if err := n.flush(b == '\r' || b == '\n'); err != nil {
		return err
	}
	switch {
	case b == '\r':
		n.sb.Delete(1) // removed CR; following LF (if any) supplies the '\n'
		n.cr = true
	case b == '\n':
		if err := n.emit('\n', off); err != nil {
			return err
		}
		n.sb.Direct(1)
	default:
		if err := n.emit(b, off); err != nil {
			return err
		}
	n.sb.Direct(1)
	}
	n.off++
	return nil
}

// Write feeds a chunk; splitting a stream arbitrarily never changes output.
func (n *Normalizer) Write(p []byte) (int, error) {
	for i, b := range p {
		if err := n.writeByte(b); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

// trimTo truncates output at output cut k, dropping entries strictly beyond
// it (deletion/insertion boundary entries are handled conservatively).
func (n *Normalizer) trimTo(k int) {
	if k == len(n.out) {
		return
	}
	m := n.sb.Map()
	origCut := m.ToOrig(k)
	var es []span.Entry
	for _, e := range m.Entries() {
		switch {
		case e.OutHi <= k && e.OrigHi <= origCut:
			es = append(es, e) // wholly before the cut
		case e.OutLo < k && e.OrigLo < origCut &&
			e.OrigHi-e.OrigLo == e.OutHi-e.OutLo && e.OrigHi-e.OrigLo > 0:
			d := k - e.OutLo
			es = append(es, span.Entry{e.OrigLo, e.OrigLo + d, e.OutLo, k})
		}
	}
	n.sb.Use(es, k)
	n.out = n.out[:k]
}

// Close resolves pending state and applies the final newline policy.
func (n *Normalizer) Close() error {
	if n.terminal {
		return nil
	}
	n.terminal = true
	if n.cr {
		if err := n.emit('\n', n.off); err != nil {
			return err
		}
		n.sb.Insert(n.off)
		n.cr = false
	}
	if err := n.flush(true); err != nil { // trailing run at EOF is deleted
		return err
	}
	switch n.cfg.Final {
	case One:
		if n.off > 0 {
			k := len(n.out)
			for k > 0 && n.out[k-1] == '\n' {
				k--
			}
			n.trimTo(k)
			if err := n.emit('\n', n.off); err != nil {
				return err
			}
			n.sb.Insert(n.off)
		}
	case Trim:
		k := len(n.out)
		for k > 0 && n.out[k-1] == '\n' {
			k--
		}
		if k == 0 {
			n.trimTo(0) // only blank lines -> empty
		} else {
			n.trimTo(k)
			if err := n.emit('\n', n.off); err != nil {
				return err
			}
			n.sb.Insert(n.off)
		}
	}
	return nil
}

// Output returns the normalized bytes (after Close).
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the bidirectional offset map (after Close).
func (n *Normalizer) Map() *span.Map { return n.sb.Map() }

// Normalize normalizes a complete buffer in one call.
func Normalize(p []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(p); err != nil {
		return n.Output(), n.Map(), err
	}
	err := n.Close()
	return n.Output(), n.Map(), err
}
