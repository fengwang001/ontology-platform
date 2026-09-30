// Package norm streams line-ending/trailing-whitespace normalization with a
// bidirectional offset map. A Normalizer is not goroutine-safe.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy selects end-of-file newline handling.
type Policy uint8

const (
	Keep      Policy = iota // leave trailing newlines as produced
	EnsureOne               // non-empty output ends with exactly one \n
	TrimEmpty               // drop trailing empty lines; one \n after content
)

// Config configures a Normalizer; zero value is Keep with no limits.
type Config struct {
	EndPolicy      Policy
	StrictNUL      bool
	SpaceBufferMax int // <=0: unlimited
	OutputMax      int // <=0: unlimited
}

var (
	ErrNUL         = errors.New("norm: NUL byte")
	ErrSpaceBuffer = errors.New("norm: trailing whitespace buffer overflow")
	ErrOutputLimit = errors.New("norm: output limit exceeded")
	ErrClosed      = errors.New("norm: write after close/error")
)

// OffsetError wraps a sentinel with the failure's original offset.
type OffsetError struct {
	Err error
	At  int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer streams bytes into normalized output plus a span.Map.
type Normalizer struct {
	cfg          Config
	sp           eol.Splitter
	wd           ws.Decider
	out          []byte
	mp           span.Map
	ev           []eol.Event
	done, failed bool
	lastI, lastO int
	pos          int
	// Fragment keeps a trailing \r/undecided spaces raw at Close for joining.
	Fragment bool
	tailRaw  int // fragment-mode: count of undecided raw original bytes at tail
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

// Write feeds original bytes; a terminal error preserves prior output.
func (n *Normalizer) Write(b []byte) (int, error) {
	if n.done {
		return 0, ErrClosed
	}
	for i, c := range b {
		if n.cfg.StrictNUL && c == 0 {
			return i, n.failf(ErrNUL, n.pos+i)
		}
	}
	n.pos += len(b)
	n.ev = n.sp.Feed(b, n.ev[:0])
	for _, ev := range n.ev {
		for _, it := range n.wd.Push(ev, nil) {
			if err := n.emit(it); err != nil {
				return 0, err
			}
		}
	}
	if lim := n.cfg.SpaceBufferMax; lim > 0 && len(n.wd.Pending()) > lim {
		return 0, n.failf(ErrSpaceBuffer, n.sp.Pos())
	}
	return len(b), nil
}

// Close flushes pending state and applies the end policy (unless Fragment).
func (n *Normalizer) Close() error {
	if n.done {
		if n.failed {
			return ErrClosed
		}
		return nil
	}
	n.done = true
	if err := n.flushTail(); err != nil {
		n.failed = true
		return err
	}
	if !n.Fragment {
		n.applyPolicy()
	}
	return nil
}

// Output returns normalized bytes.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the offset map.
func (n *Normalizer) Map() *span.Map { return &n.mp }

func (n *Normalizer) emit(it ws.Item) error {
	if n.lastI < it.Orig {
		n.mp.Add(n.lastO, n.lastI, 0, it.Orig-n.lastI)
		n.lastI = it.Orig
	}
	switch {
	case it.Drop:
		n.mp.Add(n.lastO, it.Orig, 0, 1)
	case it.NL:
		n.out = append(n.out, '\n')
		if lim := n.cfg.OutputMax; lim > 0 && len(n.out) > lim {
			return n.failf(ErrOutputLimit, it.Orig+it.Len)
		}
		if it.Len > 1 { // \r in \r\n folds onto the \n position
			n.mp.Add(n.lastO, it.Orig, 0, it.Len-1)
			n.lastI = it.Orig + it.Len - 1
		}
		n.mp.Add(n.lastO, it.Orig+it.Len-1, 1, 1)
		n.lastO++
	default:
		n.out = append(n.out, it.Byte)
		if lim := n.cfg.OutputMax; lim > 0 && len(n.out) > lim {
			return n.failf(ErrOutputLimit, it.Orig+1)
		}
		n.mp.Add(n.lastO, it.Orig, 1, 1)
		n.lastO++
	}
	n.lastI = it.Orig + it.Len
	return nil
}

func (n *Normalizer) failf(s error, at int) error {
	n.failed, n.done = true, true
	return &OffsetError{Err: s, At: at}
}
