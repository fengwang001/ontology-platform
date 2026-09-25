// Package norm streams normalized line endings and trailing whitespace.
package norm

import (
	"errors"
	"strconv"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type EndPolicy uint8

const (
	Keep EndPolicy = iota
	EnsureOne
	TrimBlank
)

// Config: zero limits mean unlimited.
type Config struct {
	End            EndPolicy
	MaxTrailingRun int
	MaxOutput      int
	StrictNUL      bool
}

var (
	ErrClosed             = errors.New("norm: normalizer closed")
	ErrTrailingRunTooLong = errors.New("norm: trailing whitespace run exceeds limit")
	ErrOutputLimit        = errors.New("norm: output exceeds limit")
	ErrNUL                = errors.New("norm: NUL byte in strict mode")
)

// OffsetError carries the original-stream offset of a fatal error.
type OffsetError struct {
	Op     string
	Offset int
	Err    error
}

func (e *OffsetError) Error() string {
	return e.Op + " at orig offset " + strconv.Itoa(e.Offset) + ": " + e.Err.Error()
}
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer is not safe for concurrent use by multiple goroutines.
type Normalizer struct {
	cfg     Config
	dec     eol.Decoder
	run     ws.Run
	out     []byte
	tab     span.Table
	origPos int
	closed  bool
	fatal   error
}

func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

func (n *Normalizer) emit(b byte) error {
	if n.cfg.MaxOutput > 0 && len(n.out) >= n.cfg.MaxOutput {
		return &OffsetError{"output", n.origPos, ErrOutputLimit}
	}
	n.out = append(n.out, b)
	n.tab.Append(n.origPos, len(n.out)-1, 1, 1)
	n.origPos++
	return nil
}

func (n *Normalizer) flushRun() {
	b := n.run.ResolveContent()
	if n.cfg.MaxOutput > 0 && len(n.out)+len(b) > n.cfg.MaxOutput {
		n.fatal = &OffsetError{"output", n.origPos, ErrOutputLimit}
		return
	}
	n.out = append(n.out, b...)
	n.tab.Append(n.origPos, n.tab.OutLen(), len(b), len(b))
	n.origPos += len(b)
}

// endLine drops trailing spaces; consumeCR marks the deleted '\r' of CRLF.
func (n *Normalizer) endLine(consumeCR bool) error {
	if n.run.Len() > 0 {
		n.tab.Append(n.origPos-n.run.Len(), n.tab.OutLen(), n.run.Len(), 0)
		n.origPos += n.run.Len()
		n.run.Reset()
	}
	if consumeCR {
		n.tab.Append(n.origPos, n.tab.OutLen(), 1, 0)
		n.origPos++
	}
	return n.emit('\n')
}

func (n *Normalizer) feed(b byte) error {
	if b == 0 && n.cfg.StrictNUL {
		return &OffsetError{"nul", n.origPos, ErrNUL}
	}
	ev, pending := n.dec.Feed(b)
	switch ev {
	case eol.EventPending:
	case eol.EventCRLF:
		return n.endLine(true)
	case eol.EventLF:
		return n.endLine(false)
	case eol.EventData:
		if n.dec.Pending() && !pending {
			if err := n.emit('\n'); err != nil {
				return err
			}
		}
		if ws.IsSpace(b) {
			if n.cfg.MaxTrailingRun > 0 && n.run.Len() >= n.cfg.MaxTrailingRun {
				return &OffsetError{"whitespace", n.origPos, ErrTrailingRunTooLong}
			}
			n.run.Add(b)
			return nil
		}
		n.flushRun()
		if n.fatal != nil {
			return n.fatal
		}
		return n.emit(b)
	}
	return nil
}

// Write feeds bytes; after Close or a fatal error it returns ErrClosed.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.fatal != nil {
		return 0, ErrClosed
	}
	for i, b := range p {
		if err := n.feed(b); err != nil {
			n.fatal = err
			return i, err
		}
	}
	return len(p), nil
}

// Close finalizes a dangling '\r', EOF trailing spaces, and the end policy.
func (n *Normalizer) Close() error {
	if n.closed {
		return ErrClosed
	}
	n.closed = true
	if n.fatal != nil {
		return n.fatal
	}
	if n.dec.Flush() == eol.EventLF {
		if err := n.emit('\n'); err != nil {
			n.fatal = err
			return err
		}
	}
	if n.run.Len() > 0 {
		n.tab.Append(n.origPos-n.run.Len(), n.tab.OutLen(), n.run.Len(), 0)
		n.origPos += n.run.Len()
		n.run.Reset()
	}
	switch n.cfg.End {
	case EnsureOne:
		if len(n.out) == 0 || n.out[len(n.out)-1] != '\n' {
			n.out = append(n.out, '\n')
			n.tab.Append(n.origPos, len(n.out)-1, 0, 1)
		}
	case TrimBlank:
		p := len(n.out)
		for p > 0 && n.out[p-1] == '\n' {
			p--
		}
		n.out = n.out[:p]
		n.tab.Trim(p)
		if p > 0 {
			n.out = append(n.out, '\n')
			n.tab.Append(n.tab.OrigLen(), len(n.out)-1, 0, 1)
		}
	}
	return nil
}

// Output returns a copy of the normalized bytes produced so far.
func (n *Normalizer) Output() []byte {
	out := make([]byte, len(n.out))
	copy(out, n.out)
	return out
}

func (n *Normalizer) Map() *span.Table { return &n.tab }
