// Package norm is a streaming normalizer for mixed line endings and trailing
// whitespace with a bidirectional offset map. A Normalizer is not safe for
// concurrent use; use separate instances for parallelism.
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
	Preserve  Policy = iota // keep the original trailing newline run
	EnsureOne               // exactly one trailing newline; empty input stays empty
	TrimBlank               // drop trailing blank lines, guarantee one newline
)

// Sentinels are wrapped in OffsetError to carry the original byte offset.
var (
	ErrNUL         = errors.New("norm: NUL byte in strict mode")
	ErrWSOverflow  = errors.New("norm: pending whitespace over limit")
	ErrOutputLimit = errors.New("norm: output exceeds limit")
	ErrClosed      = errors.New("norm: write after terminal state")
)

// OffsetError pairs a sentinel with the original offset where it arose.
type OffsetError struct {
	Err error
	Off int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Config configures a normalizer; zero-value limits mean unlimited.
type Config struct {
	Tail       Policy
	StrictNUL  bool
	MaxPending int
	MaxOutput  int
}

// Normalizer is the streaming state machine.
type Normalizer struct {
	cfg            Config
	dec            eol.Decoder
	track          ws.Tracker
	out, tail      []byte
	sb             *span.Builder
	pos            int
	closed, failed bool
}

// New creates a normalizer.
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, sb: span.NewBuilder()}
}

func (n *Normalizer) emit(b byte) error {
	if n.cfg.MaxOutput > 0 && len(n.out) >= n.cfg.MaxOutput {
		return &OffsetError{Err: ErrOutputLimit, Off: n.pos}
	}
	n.out = append(n.out, b)
	n.sb.Keep(1)
	return nil
}

func (n *Normalizer) flushWS(trailing bool) {
	run, _ := n.track.Run()
	if trailing {
		n.sb.Delete(len(run))
		n.track.Drop()
		return
	}
	n.out = append(n.out, run...)
	n.sb.Keep(len(run))
	n.track.Keep()
}

func (n *Normalizer) onEvent(ev eol.Event) error {
	switch ev.Kind {
	case eol.Lit:
		if n.cfg.StrictNUL && ev.Byte == 0 {
			return &OffsetError{Err: ErrNUL, Off: ev.Pos}
		}
		if ev.Byte == ' ' || ev.Byte == '\t' {
			n.track.Add(ev.Byte, ev.Pos)
			if n.cfg.MaxPending > 0 && len(n.track.Bytes()) > n.cfg.MaxPending {
				return &OffsetError{Err: ErrWSOverflow, Off: ev.Pos}
			}
			return nil
		}
		if n.track.Pending() {
			n.flushWS(false)
		}
		return n.emit(ev.Byte)
	case eol.CRLF:
		if n.track.Pending() {
			n.flushWS(true)
		}
		n.sb.Delete(1)
		return n.emit('\n')
	case eol.CR, eol.LF:
		if n.track.Pending() {
			n.flushWS(true)
		}
		return n.emit('\n')
	}
	return nil
}

// Write feeds a chunk; every chunking of an input yields identical output.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed {
		return 0, &OffsetError{Err: ErrClosed, Off: n.pos}
	}
	for k, b := range p {
		e1, e2, two := n.dec.Feed(b)
		n.pos++
		err := error(nil)
		if two {
			err = n.onEvent(e2)
		}
		if err == nil && (two || e1 != (eol.Event{})) {
			err = n.onEvent(e1)
		}
		if err != nil {
			n.failed, n.closed = true, true
			return k, err
		}
	}
	return len(p), nil
}

// Close resolves a trailing CR / pending whitespace and applies the policy.
func (n *Normalizer) Close() error {
	if n.closed {
		return &OffsetError{Err: ErrClosed, Off: n.pos}
	}
	n.closed = true
	if ev, ok := n.dec.Flush(); ok {
		if err := n.onEvent(ev); err != nil {
			n.failed = true
			return err
		}
	}
	if n.track.Pending() {
		n.flushWS(true)
	}
	n.applyTail()
	return nil
}

func (n *Normalizer) applyTail() {
	if n.cfg.Tail == Preserve {
		n.tail = append([]byte(nil), n.out...)
		return
	}
	i := len(n.out)
	for i > 0 && n.out[i-1] == '\n' {
		i--
	}
	body := n.out[:i]
	wantNL := n.cfg.Tail == EnsureOne && len(n.out) > i
	if n.cfg.Tail == TrimBlank {
		wantNL = len(body) > 0
	}
	tmp := n.sb.Build()
	origAt := tmp.ToOrig(len(body))
	n.sb.Truncate(origAt, len(body))
	n.tail = append([]byte(nil), body...)
	if wantNL {
		n.sb.Insert(1)
		n.tail = append(n.tail, '\n')
	}
}

// Output returns the normalized bytes (valid after Close).
func (n *Normalizer) Output() []byte { return n.tail }

// Map returns the finalized bidirectional map (valid after Close).
func (n *Normalizer) Map() span.Map { return n.sb.Build() }

// Run normalizes a whole buffer in one shot.
func Run(p []byte, cfg Config) ([]byte, span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(p); err != nil {
		return append([]byte(nil), n.out...), n.sb.Build(), err
	}
	err := n.Close()
	return append([]byte(nil), n.tail...), n.sb.Build(), err
}
