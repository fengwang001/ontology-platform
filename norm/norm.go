// Package norm is a streaming normalizer of line endings and trailing
// whitespace with a bidirectional offset map. A single Normalizer is not
// safe for concurrent use.
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
	Keep      Policy = iota // leave K(x) as is
	EnsureOne               // empty input -> "\n"; otherwise exactly one trailing \n
	TrimEmpty               // collapse existing trailing newlines to one; add none
)

// Config configures a Normalizer. Zero limits mean unlimited.
type Config struct {
	End        Policy
	MaxPending int
	MaxOutput  int
	StrictNUL  bool
}

var (
	ErrNUL          = errors.New("norm: NUL byte in strict mode")
	ErrPendingLimit = errors.New("norm: pending whitespace limit exceeded")
	ErrOutputLimit  = errors.New("norm: output size limit exceeded")
	ErrClosed       = errors.New("norm: normalizer is in a terminal state")
)

// OffsetError wraps a sentinel with the original byte offset that triggered it.
type OffsetError struct {
	Err error
	Off int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Offset returns the triggering original byte offset.
func (e *OffsetError) Offset() int { return e.Off }

// Normalizer consumes bytes via Write and finalizes via Close.
type Normalizer struct {
	cfg                            Config
	out                            []byte
	mp                             span.Map
	pendingCR                      bool
	crPos, wsStart, origPos, outP  int
	pendingWS                      []byte
	done, fatal                    bool
}

// New returns a Normalizer for cfg.
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg, wsStart: -1} }

func (n *Normalizer) fail(err error, off int) error {
	n.fatal = true
	return &OffsetError{Err: err, Off: off}
}

func (n *Normalizer) keep(data []byte, o0 int) {
	n.out = append(n.out, data...)
	n.mp.Add(o0, o0+len(data), n.outP, n.outP+len(data))
	n.outP += len(data)
}

func (n *Normalizer) nl(o int) {
	n.out = append(n.out, '\n')
	n.mp.Add(o, o+1, n.outP, n.outP+1)
	n.outP++
}

func (n *Normalizer) dropWS() {
	if len(n.pendingWS) > 0 {
		n.mp.Add(n.wsStart, n.wsStart+len(n.pendingWS), n.outP, n.outP)
		n.pendingWS = n.pendingWS[:0]
	}
	n.wsStart = -1
}

func (n *Normalizer) emitWS() {
	if len(n.pendingWS) > 0 {
		n.keep(n.pendingWS, n.wsStart)
		n.pendingWS = n.pendingWS[:0]
	}
	n.wsStart = -1
}

func (n *Normalizer) step(b byte, i int) error {
	if n.pendingCR {
		n.pendingCR = false
		if b == eol.LF {
			n.mp.Add(n.crPos, n.crPos+1, n.outP, n.outP) // CRLF: drop CR
			n.nl(i)
			return n.over(i)
		}
		n.nl(n.crPos) // lone CR
	}
	switch {
	case ws.IsWS(b):
		if len(n.pendingWS) == 0 {
			n.wsStart = i
		}
		n.pendingWS = append(n.pendingWS, b)
		if n.cfg.MaxPending > 0 && len(n.pendingWS) > n.cfg.MaxPending {
			return n.fail(ErrPendingLimit, i)
		}
	case b == eol.CR:
		n.dropWS()
		n.pendingCR, n.crPos = true, i
	case b == eol.LF:
		n.dropWS()
		n.nl(i)
	default:
		if b == 0 && n.cfg.StrictNUL {
			return n.fail(ErrNUL, i)
		}
		n.emitWS()
		n.keep([]byte{b}, i)
	}
	return n.over(i)
}

func (n *Normalizer) over(i int) error {
	if n.cfg.MaxOutput > 0 && n.outP > n.cfg.MaxOutput {
		return n.fail(ErrOutputLimit, i)
	}
	return nil
}

// Write feeds bytes. A terminal error leaves already-confirmed output intact.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.fatal || n.done {
		return 0, &OffsetError{Err: ErrClosed, Off: n.origPos}
	}
	for k, b := range p {
		i := n.origPos + k
		if err := n.step(b, i); err != nil {
			n.origPos = i + 1
			return k + 1, err
		}
	}
	n.origPos += len(p)
	return len(p), nil
}

// Close resolves pending state and applies the end policy.
func (n *Normalizer) Close() error {
	if n.fatal {
		return &OffsetError{Err: ErrClosed, Off: n.origPos}
	}
	if n.done {
		return nil
	}
	if n.pendingCR {
		n.pendingCR = false
		n.nl(n.crPos)
	}
	n.dropWS()
	switch n.cfg.End {
	case EnsureOne:
		u := n.outP
		for u > 0 && n.out[u-1] == '\n' {
			u--
		}
		o := n.mp.ToOrig(u)
		n.mp.Cut(o, u, u+1)
		n.out = append(n.out[:u], '\n')
		n.outP = u + 1
	case TrimEmpty:
		u := n.outP
		for u > 0 && n.out[u-1] == '\n' {
			u--
		}
		if u < n.outP {
			o := n.mp.ToOrig(u)
			n.mp.Cut(o, u, u)
			n.mp.Add(o, o+1, u, u+1)
			n.out = append(n.out[:u], '\n')
			n.outP = u + 1
		}
	}
	n.done = true
	return nil
}

// Output returns the normalized bytes.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the offset map.
func (n *Normalizer) Map() *span.Map { return &n.mp }

// Normalize runs one buffer to completion.
func Normalize(data []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(data); err != nil {
		return n.Output(), n.Map(), err
	}
	if err := n.Close(); err != nil {
		return n.Output(), n.Map(), err
	}
	return n.Output(), n.Map(), nil
}
