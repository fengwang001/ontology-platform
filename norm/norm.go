// Package norm is a streaming line-ending and trailing-whitespace normalizer
// with a bidirectional offset map (see package span). A single Normalizer is
// not safe for concurrent use; use multiple instances for concurrency.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy selects the end-of-file newline behavior.
type Policy int

const (
	Keep Policy = iota // leave the tail exactly as normalized
	One                // exactly one trailing newline on non-empty output
	Trim               // no trailing blank lines; one newline after real content
)

// Config configures a Normalizer. Zero values mean unlimited except Strict.
type Config struct {
	Ending   Policy
	Strict   bool // fail on NUL
	WSLimit  int  // max deferred whitespace bytes; <=0 unlimited
	OutLimit int  // max output bytes; <=0 unlimited
	Open     bool // chunk mode: no end-of-file decision on Close
}

var (
	// ErrNUL is wrapped (with an OffsetError) in strict mode on a NUL byte.
	ErrNUL = errors.New("norm: NUL byte")
	// ErrWSLimit is returned when deferred whitespace exceeds WSLimit.
	ErrWSLimit = errors.New("norm: whitespace buffer limit")
	// ErrOutLimit is returned when output would exceed OutLimit.
	ErrOutLimit = errors.New("norm: output limit")
	// ErrClosed is returned after a terminal error or Close.
	ErrClosed = errors.New("norm: normalizer closed")
)

// OffsetError pairs a sentinel cause with the original offset.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

func errAt(cause error, off int) error { return &OffsetError{Err: cause, Offset: off} }

// Normalizer consumes a byte stream via Write and yields output via Output.
type Normalizer struct {
	cfg   Config
	out   []byte
	map_  span.Mapper
	pend  ws.Run
	cr    bool
	off   int
	ended bool
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

func (n *Normalizer) emit(b byte) error {
	if n.cfg.OutLimit > 0 && len(n.out)+1 > n.cfg.OutLimit {
		n.ended = true
		return errAt(ErrOutLimit, n.off)
	}
	n.out = append(n.out, b)
	return nil
}

func (n *Normalizer) flushWS() error {
	for i := 0; i < n.pend.Len(); i++ {
		if err := n.emit(n.pend.Data[i]); err != nil {
			return err
		}
		n.map_.Keep(1)
	}
	n.pend.Reset()
	return nil
}

func (n *Normalizer) dropWS() {
	if n.pend.Len() > 0 {
		n.map_.Delete(n.pend.Len())
		n.pend.Reset()
	}
}

func (n *Normalizer) emitNL(remap bool) error {
	if remap {
		n.map_.Remap(1)
	} else {
		n.map_.Keep(1)
	}
	return n.emit('\n')
}

// Write feeds bytes. It returns a terminal error on the first violation.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.ended {
		return 0, ErrClosed
	}
	for i := 0; i < len(p); i++ {
		b := p[i]
		if b == 0 && n.cfg.Strict {
			n.ended = true
			return i, errAt(ErrNUL, n.off)
		}
		k := eol.Classify(b, n.cr)
		switch {
		case k == eol.Pending:
			n.dropWS()
			n.map_.Barrier() // close the whitespace deletion before CR-delete
			if n.cr {
				n.map_.Remap(1)
				if err := n.emit('\n'); err != nil {
					n.ended = true
					return i, err
				}
			}
			n.cr = true
		case k == eol.CRLF:
			n.cr = false
			n.dropWS()
			n.map_.Barrier()
			n.map_.Delete(1)
			if err := n.emit('\n'); err != nil {
				n.ended = true
				return i, err
			}
		case k == eol.LF:
			n.dropWS()
			n.map_.Barrier()
			n.map_.Keep(1)
			if err := n.emit('\n'); err != nil {
				n.ended = true
				return i, err
			}
		case ws.IsSpace(b):
			if n.cr {
				n.cr = false
				n.map_.Remap(1)
				if err := n.emit('\n'); err != nil {
					n.ended = true
					return i, err
				}
			}
			if n.cfg.WSLimit > 0 && n.pend.Len()+1 > n.cfg.WSLimit {
				n.ended = true
				return i, errAt(ErrWSLimit, n.off)
			}
			n.pend.Append(b, n.off)
		default:
			if n.cr {
				n.cr = false
				if err := n.emitNL(true); err != nil {
					n.ended = true
					return i, err
				}
			}
			if err := n.flushWS(); err != nil {
				n.ended = true
				return i, err
			}
			if err := n.emit(b); err != nil {
				n.ended = true
				return i, err
			}
			n.map_.Keep(1)
		}
		n.off++
	}
	return len(p), nil
}

// Snapshot exposes chunk state for the par package.
type Snapshot struct {
	Out       []byte
	Map       *span.Mapper
	CR        bool
	WS        []byte
	WSStart   int
	OrigStart int
}

// OpenSnapshot resolves only a pending CR and returns the open-mode state.
func (n *Normalizer) OpenSnapshot() Snapshot {
	return Snapshot{Out: append([]byte(nil), n.out...), Map: &n.map_,
		CR: n.cr, WS: append([]byte(nil), n.pend.Data...), WSStart: n.pend.Start}
}

// Close applies the ending policy (unless Open) and finishes the stream.
func (n *Normalizer) Close() error {
	if n.ended {
		return ErrClosed
	}
	n.ended = true
	if n.cfg.Open {
		return nil
	}
	if n.cr {
		n.cr = false
		if err := n.emitNL(true); err != nil {
			return err
		}
	}
	n.dropWS()
	n.out = Finish(n.cfg.Ending, n.out, &n.map_)
	return nil
}

// Output returns the normalized bytes produced so far.
func (n *Normalizer) Output() []byte { return append([]byte(nil), n.out...) }

// Mapper returns the live offset mapper.
func (n *Normalizer) Mapper() *span.Mapper { return &n.map_ }

// ToOrig / ToOut query the offset map.
func (n *Normalizer) ToOrig(o int) int { return n.map_.ToOrig(o) }
func (n *Normalizer) ToOut(i int) int  { return n.map_.ToOut(i) }

// Finish applies the ending policy to an assembled stream. out is the
// normalized prefix; the returned slice is the policy-adjusted result and m
// is mutated consistently. wsBorrow reports whether a trailing deleted byte
// is available to source a synthetic newline.
func Finish(p Policy, out []byte, m *span.Mapper) []byte {
	tail := len(out)
	for tail > 0 && out[tail-1] == '\n' {
		tail--
	}
	nl := len(out) - tail
	switch p {
	case Keep:
		return out
	case One:
		if len(out) == 0 {
			return out
		}
		if nl > 1 {
			m.TrimOutput(nl - 1)
			out = out[:tail+1]
		} else if nl == 0 {
			addNL(m)
			out = append(out, '\n')
		}
	case Trim:
		if tail == 0 {
			if nl > 0 {
				m.TrimOutput(nl)
			}
			return out[:0]
		}
		if nl > 1 {
			m.TrimOutput(nl - 1)
			out = out[:tail+1]
		} else if nl == 0 {
			addNL(m)
			out = append(out, '\n')
		}
	}
	return out
}

func addNL(m *span.Mapper) {
	if !m.Borrow() {
		m.Synth(m.OrigLen())
	}
}
