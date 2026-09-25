// Package norm is a streaming normalizer for mixed line endings and trailing
// whitespace, with exact two-way byte-offset mapping. A single Normalizer is
// not safe for concurrent use; use separate instances per goroutine.
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
	Keep      Ending = iota // keep the normalized tail
	EnsureOne               // non-empty output ends with exactly one \n
	TrimEmpty               // drop trailing blank lines; keep one \n if content
)

// Config configures a Normalizer.
type Config struct {
	Ending     Ending
	StrictNUL  bool
	MaxPending int  // buffered trailing spaces/tabs; 0 = unlimited
	MaxOutput  int  // emitted bytes; 0 = unlimited
	StartOrig  int  // original offset of the first input byte
	Stage      bool // par mode: leave a trailing \r / spaces pending
}

var (
	ErrClosed    = errors.New("norm: write/close after terminal state")
	ErrOutputMax = errors.New("norm: output limit exceeded")
	ErrPending   = errors.New("norm: pending-whitespace limit exceeded")
	errNUL       = errors.New("norm: NUL byte in strict mode")
)

// OffsetError is a detectable error carrying the original byte offset.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// IsNUL reports whether err is a strict-mode NUL rejection.
func IsNUL(err error) bool {
	var e *OffsetError
	return errors.As(err, &e) && errors.Is(e.Err, errNUL)
}

// Normalizer normalizes one stream.
type Normalizer struct {
	cfg     Config
	sc      eol.Scanner
	buf     ws.Buffer
	wsStart int
	m       *span.Map
	out     []byte
	inPos   int // consumed input bytes (relative to StartOrig)
	crPos   int
	term    bool
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, m: span.New(cfg.StartOrig)}
}

func (n *Normalizer) die(err error, off int) error {
	n.term = true
	return &OffsetError{Err: err, Offset: off}
}

func (n *Normalizer) emit(b byte, off int) error {
	if n.cfg.MaxOutput > 0 && len(n.out) >= n.cfg.MaxOutput {
		return n.die(ErrOutputMax, off)
	}
	n.out = append(n.out, b)
	return nil
}

func (n *Normalizer) newline(off int) error {
	if n.buf.Len() > 0 {
		n.m.Delete(n.cfg.StartOrig+n.wsStart, n.cfg.StartOrig+n.wsStart+n.buf.Len())
		n.buf.Trim()
	}
	return n.emit('\n', off)
}

// Write feeds one chunk; the returned count is bytes consumed from p.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.term {
		return 0, ErrClosed
	}
	for j, b := range p {
		rel := n.inPos + j
		off := n.cfg.StartOrig + rel
		if b == 0 && n.cfg.StrictNUL {
			return j, n.die(errNUL, off)
		}
		first, second := n.sc.Feed2(b)
		if first == eol.Newline { // pending \r resolved as a lone ending
			if err := n.newline(n.crPos); err != nil {
				return j, err
			}
		}
		switch second {
		case eol.Newline:
			if err := n.newline(off); err != nil {
				return j, err
			}
		case eol.PendingCR:
			// The \r may still pair with a following \n, but buffered spaces
			// before it are already at the line end: trim them now; the
			// ending itself is emitted once the \r is resolved.
			if n.buf.Len() > 0 {
				n.m.Delete(n.cfg.StartOrig+n.wsStart, n.cfg.StartOrig+n.wsStart+n.buf.Len())
				n.buf.Trim()
			}
			n.crPos = off
		case eol.Content:
			if first == eol.Newline && b == eol.LF {
				break // \r\n: \n belongs to the already-emitted ending
			}
			if ws.Is(b) {
				if n.buf.Len() == 0 {
					n.wsStart = rel
				}
				if n.cfg.MaxPending > 0 && n.buf.Len() >= n.cfg.MaxPending {
					return j, n.die(ErrPending, off)
				}
				n.buf.Add(b)
			} else {
				for _, c := range n.buf.Keep() {
					if err := n.emit(c, off); err != nil {
						return j, err
					}
				}
				if err := n.emit(b, off); err != nil {
					return j, err
				}
			}
		}
	}
	n.inPos += len(p)
	return len(p), nil
}

// Carry returns bytes still undecided at the end of a Stage stream: a run of
// spaces/tabs and/or one trailing \r. Its original start offset is also
// returned. Carry is valid after Close in Stage mode.
func (n *Normalizer) Carry() ([]byte, int) {
	if run := n.buf.Run(); len(run) > 0 {
		return append([]byte(nil), run...), n.cfg.StartOrig + n.wsStart
	}
	if n.sc.Pending() {
		return []byte{eol.CR}, n.crPos
	}
	return nil, 0
}

// Close finalizes the stream.
func (n *Normalizer) Close() error {
	if n.term {
		return ErrClosed
	}
	n.term = true
	if n.cfg.Stage {
		return nil
	}
	if n.sc.Flush() {
		if err := n.newline(n.crPos); err != nil {
			return err
		}
	}
	if n.buf.Len() > 0 { // run reaching EOF is trailing whitespace
		n.m.Delete(n.cfg.StartOrig+n.wsStart, n.cfg.StartOrig+n.wsStart+n.buf.Len())
		n.buf.Trim()
	}
	n.applyEnding()
	n.m.Finish(n.cfg.StartOrig+n.inPos, len(n.out))
	return nil
}

func (n *Normalizer) applyEnding() {
	switch n.cfg.Ending {
	case EnsureOne:
		if len(n.out) == 0 {
			return
		}
		k := len(n.out)
		for k > 0 && n.out[k-1] == '\n' {
			k--
		}
		if k == 0 { // output was only newlines: keep one
			n.out = append(n.out[:0], '\n')
			return
		}
		n.out = append(n.out[:k], '\n')
	case TrimEmpty:
		k := len(n.out)
		for k > 0 && n.out[k-1] == '\n' {
			k--
		}
		if k > 0 {
			n.out = append(n.out[:k], '\n')
		} else {
			n.out = n.out[:0]
		}
	}
}

// Output returns the normalized bytes.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the offset map.
func (n *Normalizer) Map() *span.Map { return n.m }
