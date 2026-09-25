// Package norm is a streaming normalizer for mixed line endings and trailing
// whitespace, with a bidirectional offset map.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Ending selects the end-of-file newline policy.
type Ending int

const (
	Preserve Ending = iota // keep the original trailing newline structure
	Ensure                 // non-empty output ends with exactly one '\n'
	Trim                   // drop all blank tail lines, keep one '\n'
)

// Config configures a normalizer. Zero value means no limits.
type Config struct {
	Ending       Ending
	StrictNUL    bool // NUL byte is an error
	MaxPendingWS int  // max buffered undecided whitespace; 0 = unlimited
	MaxOutput    int  // max emitted output bytes; 0 = unlimited
}

// Sentinel errors are wrapped in OffsetError with the source offset.
var (
	ErrNUL       = errors.New("norm: NUL byte")
	ErrWSBuffer  = errors.New("norm: pending whitespace limit exceeded")
	ErrOutputCap = errors.New("norm: output limit exceeded")
	ErrClosed    = errors.New("norm: write after close/error")
)

// OffsetError pairs a sentinel with the source offset where it occurred.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer consumes bytes via Write and is finalized by Close.
// A single instance is not safe for concurrent use.
type Normalizer struct {
	cfg    Config
	sc     eol.Scanner
	wt     ws.Tracker
	mp     *span.Map
	out    []byte
	off    int // source bytes consumed
	closed bool
	err    error
}

// New constructs a Normalizer.
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, mp: span.New()}
}

func (n *Normalizer) fail(err error, off int) error {
	e := &OffsetError{Err: err, Offset: off}
	n.err, n.closed = e, true
	n.mp.Finish(n.off)
	return e
}

func (n *Normalizer) emit(b byte) error {
	if n.cfg.MaxOutput > 0 && len(n.out) >= n.cfg.MaxOutput {
		return n.fail(ErrOutputCap, n.off)
	}
	n.out = append(n.out, b)
	n.mp.Keep(1)
	return nil
}

func (n *Normalizer) flushContent(b []byte) error {
	for _, c := range b {
		if err := n.emit(c); err != nil {
			return err
		}
	}
	return nil
}

// Write feeds one chunk; any prefix is retained on error.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed {
		if n.err != nil {
			return 0, n.err
		}
		return 0, ErrClosed
	}
	for i := 0; i < len(p); i++ {
		b := p[i]
		if n.cfg.StrictNUL && b == 0 {
			return i, n.fail(ErrNUL, n.off)
		}
		ev, cr := n.sc.Feed(b)
		if cr {
			n.dropWS()
			if err := n.emit('\n'); err != nil {
				return i, err
			}
		}
		var err error
		switch ev {
		case eol.CRLF:
			err = n.crlf()
		case eol.LF:
			n.dropWS()
			err = n.emit('\n')
		case eol.HasCR:
			// buffered pending '\r'
		default:
			err = n.content(b)
		}
		if err != nil {
			return i, err
		}
		n.off++
	}
	return len(p), nil
}

func (n *Normalizer) crlf() error {
	n.dropWS()
	n.mp.Drop(1)
	return n.emit('\n')
}

func (n *Normalizer) dropWS() {
	if buf, _, ok := n.wt.Pending(); ok {
		n.mp.Drop(len(buf))
		n.wt.Drop()
	}
}

func (n *Normalizer) content(b byte) error {
	if ws.IsSpace(b) {
		if n.cfg.MaxPendingWS > 0 && n.wt.Len() >= n.cfg.MaxPendingWS {
			return n.fail(ErrWSBuffer, n.off)
		}
		n.wt.Add(b, n.off)
		return nil
	}
	if buf := n.wt.Flush(); buf != nil {
		if err := n.flushContent(buf); err != nil {
			return err
		}
	}
	return n.emit(b)
}

// Close applies the ending policy and finalizes. Repeated Close is an error.
func (n *Normalizer) Close() error {
	if n.closed {
		if n.err != nil {
			return n.err
		}
		return ErrClosed
	}
	n.closed = true
	if n.sc.Flush() {
		n.dropWS()
		if err := n.emit('\n'); err != nil {
			return err
		}
	}
	n.dropWS() // trailing whitespace on the last unterminated line
	if n.cfg.Ending != Preserve {
		cut := len(n.out)
		for cut > 0 && n.out[cut-1] == '\n' {
			cut--
		}
		if n.off > 0 {
			n.out = append(n.out[:cut], '\n')
		} else {
			n.out = n.out[:0]
		}
		n.mp = n.mp.RestrictOut(len(n.out))
	}
	n.mp.Finish(n.off)
	return nil
}

// Output returns the normalized bytes (valid after Close or on a terminal error).
func (n *Normalizer) Output() []byte { return append([]byte(nil), n.out...) }

// Map returns the offset map (final after Close).
func (n *Normalizer) Map() *span.Map { return n.mp }

// Run normalizes a complete buffer in one shot.
func Run(p []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(p); err != nil {
		return n.Output(), n.Map(), err
	}
	err := n.Close()
	return n.Output(), n.Map(), err
}
