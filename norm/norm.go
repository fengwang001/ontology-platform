// Package norm is a streaming normalizer of line endings and trailing
// whitespace, with a two-way offset map. A single Normalizer is not safe for
// concurrent Write calls.
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
	Keep      Policy = iota // leave the original end as-is
	EnsureOne               // non-empty input ends with exactly one \n
	TrimAll                 // drop all trailing blank lines; keep one \n if content remains
)

// Config bounds the normalizer. Zero means unbounded.
type Config struct {
	End       Policy
	MaxWS     int // pending-whitespace bytes before ErrWSOverflow
	MaxOut    int // total output bytes before ErrOutLimit
	StrictNUL bool
}

var (
	// ErrWSOverflow: the pending-whitespace run reached MaxWS.
	ErrWSOverflow = errors.New("norm: trailing whitespace buffer overflow")
	// ErrOutLimit: output exceeded MaxOut.
	ErrOutLimit = errors.New("norm: output size limit exceeded")
	// ErrNUL: a NUL byte arrived in strict mode.
	ErrNUL = errors.New("norm: NUL byte in strict mode")
	// ErrClosed: Write after the normalizer reached a terminal state.
	ErrClosed = errors.New("norm: write after close/terminal state")
)

// OffsetError wraps a sentinel with the original byte offset.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer is the streaming state machine.
type Normalizer struct {
	cfg           Config
	e             eol.State
	w             ws.Buf
	m             *span.Map
	out           []byte
	off           int // original bytes consumed
	closed, fatal bool
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, m: span.New()}
}

func (n *Normalizer) emit(b byte) {
	n.m.Add(n.off, len(n.out), 1)
	n.out = append(n.out, b)
	n.off++
}

func (n *Normalizer) emitBytes(p []byte) {
	for _, b := range p {
		n.emit(b)
	}
}

func (n *Normalizer) fail(err error) error {
	n.fatal = true
	return &OffsetError{Err: err, Offset: n.off}
}

// Write feeds a chunk. On error the produced prefix stays available and the
// normalizer becomes terminal.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.fatal {
		return 0, &OffsetError{Err: ErrClosed, Offset: n.off}
	}
	for i := 0; i < len(p); i++ {
		if n.cfg.StrictNUL && p[i] == 0 {
			return i, n.fail(ErrNUL)
		}
		consumed, newline := n.e.Feed(p[i])
		if consumed == 1 { // pending \r settles first
			n.w.Drop()
			// standalone \r: the converted newline is anchored at the \r.
			if err := n.putNL(n.off - 1); err != nil {
				return i, err
			}
		}
		switch {
		case newline:
			n.w.Drop()
			anchor := n.off - 1 // \r\n: the \n output belongs to the \r
			if consumed == 0 {
				anchor = n.off // bare \n: belongs to itself
			}
			if err := n.putNL(anchor); err != nil {
				return i, err
			}
			n.off++ // the \n byte itself is consumed (deleted in \r\n)
		case ws.IsSpace(p[i]):
			if n.w.Overflow(n.cfg.MaxWS) {
				return i, n.fail(ErrWSOverflow)
			}
			n.w.Add(p[i])
			n.off++
		default:
			if pending := n.w.Take(); pending != nil {
				n.emitBytes(pending)
			}
			n.emit(p[i])
		}
	}
	return len(p), nil
}

func (n *Normalizer) putNL(anchor int) error {
	if n.cfg.MaxOut > 0 && len(n.out) >= n.cfg.MaxOut {
		return n.fail(ErrOutLimit)
	}
	n.out = append(n.out, '\n')
	n.m.Add(anchor, len(n.out)-1, 1)
	return nil
}

// Close flushes the pending \r (as a newline) and applies the end policy.
func (n *Normalizer) Close() error {
	if n.fatal {
		return &OffsetError{Err: ErrClosed, Offset: n.off}
	}
	if n.closed {
		return nil
	}
	n.closed = true
	if n.e.Flush() {
		n.w.Drop()
		if err := n.putNL(n.off - 1); err != nil {
			return err
		}
	}
	if pending := n.w.Take(); pending != nil {
		n.emitBytes(pending) // inline whitespace survives
	}
	n.applyPolicy()
	if n.fatal {
		return &OffsetError{Err: ErrOutLimit, Offset: n.off}
	}
	return nil
}

func (n *Normalizer) applyPolicy() {
	out, fatal := ApplyEnd(n.cfg, n.out, n.m)
	n.out, n.fatal = out, fatal
}

func (n *Normalizer) appendNL() {
	if n.cfg.MaxOut > 0 && len(n.out) >= n.cfg.MaxOut {
		n.fatal = true
		return
	}
	n.out = append(n.out, '\n')
	n.m.SetTail(len(n.out) - 1)
}

// ApplyEnd mutates out and m according to p.End. It returns the resulting
// bytes and whether MaxOut was exceeded. It is shared by streaming Close and
// by par after stitching.
func ApplyEnd(cfg Config, out []byte, m *span.Map) ([]byte, bool) {
	k := len(out)
	for k > 0 && out[k-1] == '\n' {
		k--
	}
	switch cfg.End {
	case Keep:
		return out, false
	case EnsureOne:
		if len(out) == 0 {
			return out, false
		}
		if k < len(out) {
			m.TrimTo(m.ToOrig(k), k)
			out = out[:k]
		}
		if cfg.MaxOut > 0 && len(out) >= cfg.MaxOut {
			return out, true
		}
		out = append(out, '\n')
		m.SetTail(len(out) - 1)
		return out, false
	case TrimAll:
		if k == 0 {
			m.TrimTo(0, 0)
			return out[:0], false
		}
		m.TrimTo(m.ToOrig(k), k)
		out = out[:k]
		if cfg.MaxOut > 0 && len(out) >= cfg.MaxOut {
			return out, true
		}
		out = append(out, '\n')
		m.SetTail(len(out) - 1)
		return out, false
	}
	return out, false
}

// Output returns the normalized bytes produced so far.
func (n *Normalizer) Output() []byte { return n.out }

// Map returns the offset map (stable after Close).
func (n *Normalizer) Map() *span.Map { return n.m }

// Fragment is the Keep-normalized state of one independently processed chunk.
// The chunk is fully flushed: a trailing pending \r becomes a final \n and
// trailing-inline whitespace is kept, so Out is exactly Keep(chunk).
type Fragment struct {
	Out    []byte
	Map    *span.Map
	NIn    int
	HeadIn int  // original offset of the first kept byte (-1 if none)
	PenR   bool // chunk's last byte was a standalone \r (flushed as final \n)
	PenW   int  // trailing inline-whitespace bytes kept at the end
	Err    error
}

// Process normalizes one full chunk with the Keep policy. It never applies
// EnsureOne/TrimAll; par applies the requested end policy once at the end.
func Process(cfg Config, p []byte) (Fragment, error) {
	c := cfg
	c.End = Keep
	n := New(c)
	penR := false
	if len(p) > 0 {
		// Feed all but let the last byte's pending \r be observed.
		if _, err := n.Write(p); err != nil {
			return Fragment{}, err
		}
		penR = n.e.Pending()
	}
	if penR {
		n.e.Flush()
		if err := n.putNL(n.off - 1); err != nil {
			return Fragment{}, err
		}
	}
	if pending := n.w.Take(); pending != nil {
		n.emitBytes(pending)
	}
	headIn := -1
	if len(n.out) > 0 {
		headIn = n.m.ToOrig(0)
	}
	penW := 0
	if len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
		for penW < len(n.out) {
			c := n.out[len(n.out)-1-penW]
			if c != ' ' && c != '\t' {
				break
			}
			penW++
		}
	}
	return Fragment{
		Out:    n.out,
		Map:    n.m,
		NIn:    n.off,
		HeadIn: headIn,
		PenR:   penR,
		PenW:   penW,
	}, nil
}
