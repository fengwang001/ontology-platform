// Package norm is a streaming normalizer for mixed line endings and
// trailing horizontal whitespace, with bidirectional offset mapping.
//
// A single *N is NOT safe for concurrent use; use separate instances.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy selects the end-of-file newline behavior.
type Policy uint8

const (
	// KeepEnding leaves the final newline sequence as normalized.
	KeepEnding Policy = iota
	// EnsureEnding collapses trailing newlines to at most one; none added.
	EnsureEnding
	// TrimEnding removes trailing empty lines, keeping one newline unless
	// the whole input is empty or whitespace-only.
	TrimEnding
)

var (
	// ErrNUL reports a NUL byte in strict mode.
	ErrNUL = errors.New("norm: NUL byte")
	// ErrTrailingRunLimit reports a buffered whitespace run over the cap.
	ErrTrailingRunLimit = errors.New("norm: trailing whitespace run exceeds limit")
	// ErrOutputLimit reports output exceeding the configured byte cap.
	ErrOutputLimit = errors.New("norm: output exceeds limit")
	// ErrClosed reports use of a finalized normalizer.
	ErrClosed = errors.New("norm: normalizer already finalized")
)

// PosError wraps a sentinel with the original byte offset of the fault.
type PosError struct {
	Err    error
	Offset int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

// Config configures a normalizer. Zero-value limits mean unlimited.
type Config struct {
	Ending         Policy
	StrictNUL      bool
	MaxTrailingRun int
	MaxOutput      int
}

// N is a streaming normalizer. Get one with New or Open.
type N struct {
	cfg  Config
	open bool
	done bool
	out  []byte
	m    *span.Mapper
	dec  eol.Decoder
	run  ws.Run
	pos  int
}

// New returns a closed-mode normalizer applying cfg at Close.
func New(cfg Config) *N { return &N{cfg: cfg, m: span.New()} }

// Open returns an open-mode normalizer: Close makes no stream-end or
// policy decision, exposing the pending tail for cross-segment stitching.
func Open(cfg Config) *N { return &N{cfg: cfg, open: true, m: span.New()} }

// Output returns the normalized bytes produced so far.
func (n *N) Output() []byte { return n.out }

// Map returns the offset map recorded so far.
func (n *N) Map() *span.Mapper { return n.m }

// PendingCR reports whether a CR awaits the following byte.
func (n *N) PendingCR() bool { return n.dec.Pending() }

// PendingWS returns a copy of the unresolved trailing-whitespace bytes.
func (n *N) PendingWS() []byte {
	b := n.run.Bytes()
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func (n *N) fail(err error) error {
	n.done = true
	return &PosError{Err: err, Offset: n.pos - 1}
}

func (n *N) emit(p []byte) error {
	if n.cfg.MaxOutput > 0 && len(n.out)+len(p) > n.cfg.MaxOutput {
		return n.fail(ErrOutputLimit)
	}
	n.out = append(n.out, p...)
	n.m.Copy(len(p))
	return nil
}

func (n *N) emitLF() error { return n.emit([]byte{'\n'}) }

func (n *N) endTrailing() { n.m.Delete(n.run.EndLine()) }

func (n *N) content(b byte) error {
	if ws.Is(b) {
		if n.cfg.MaxTrailingRun > 0 && n.run.Len() >= n.cfg.MaxTrailingRun {
			return n.fail(ErrTrailingRunLimit)
		}
		n.run.Add(b)
		return nil
	}
	if p := n.run.Retain(); p != nil {
		if err := n.emit(p); err != nil {
			return err
		}
	}
	if n.cfg.StrictNUL && b == 0 {
		return n.fail(ErrNUL)
	}
	return n.emit([]byte{b})
}

// Write feeds one chunk; any chunking yields identical results.
func (n *N) Write(p []byte) error {
	if n.done {
		return ErrClosed
	}
	for _, b := range p {
		n.pos++
		if err := n.feed(b); err != nil {
			return err
		}
	}
	return nil
}

func (n *N) feed(b byte) error {
	for {
		boundary, consumed := n.dec.Feed(b)
		switch boundary {
		case eol.CRLF:
			n.m.Delete(1)
			return n.emitLF()
		case eol.LoneLF:
			n.endTrailing()
			return n.emitLF()
		case eol.LoneCR:
			n.endTrailing()
			if err := n.emitLF(); err != nil {
				return err
			}
			if !consumed {
				continue // b resolved the pending CR but is itself unread
			}
			return nil
		default:
			if n.dec.Pending() {
				n.endTrailing() // CR opens a line end; preceding WS is trailing
				return nil
			}
			return n.content(b)
		}
	}
}

// Close finalizes the stream. In open mode it only freezes pending state.
func (n *N) Close() error {
	if n.done {
		return ErrClosed
	}
	n.done = true
	if n.open {
		return nil
	}
	if n.dec.Flush() == eol.LoneCR {
		if err := n.emitLF(); err != nil {
			return err
		}
	}
	n.endTrailing() // whitespace pending at EOF is trailing
	ApplyEnding(n.cfg.Ending, n)
	return nil
}

// ApplyEnding rewrites a finalized mapper/output to policy on trailing LFs.
func ApplyEnding(p Policy, n *N) {
	if p == KeepEnding {
		return
	}
	last := len(n.out) - 1
	for last >= 0 && n.out[last] == '\n' {
		last--
	}
	trailing := len(n.out) - 1 - last
	if trailing == 0 {
		return
	}
	if p == TrimEnding && last < 0 {
		n.out = n.out[:0]
		n.m.PopToOut(0)
		return
	}
	keep := last + 2 // one LF beyond the last non-newline byte
	n.out = n.out[:keep]
	n.m.PopToOut(keep)
}
