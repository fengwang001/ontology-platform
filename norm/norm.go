// Package norm is a streaming normalizer for mixed line endings and trailing
// whitespace, with an end-of-file newline policy, resource limits and a
// bidirectional byte-offset map.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy selects the end-of-stream newline behavior.
type Policy uint8

const (
	Keep Policy = iota
	EnsureOne
	TrimEmpty
)

var (
	ErrNUL             = errors.New("norm: NUL byte in strict mode")
	ErrWhitespaceLimit = errors.New("norm: trailing whitespace buffer limit exceeded")
	ErrOutputLimit     = errors.New("norm: output size limit exceeded")
	ErrClosed          = errors.New("norm: write after terminal state")
)

// OffsetError wraps a sentinel with the original byte offset of the failure.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Config configures a Normalizer. Zero limits mean unlimited.
type Config struct {
	Policy          Policy
	StrictNUL       bool
	WhitespaceLimit int
	OutputLimit     int
}

// Normalizer is a single-stream state machine. It is not safe for concurrent
// use; give each goroutine its own instance.
type Normalizer struct {
	cfg      Config
	split    eol.Splitter
	tab      span.Table
	out      []byte
	buf      []byte // undecided trailing spaces/tabs
	bufStart int    // original offset of buf[0]
	pendOff  int    // original offset of a held pending '\r', or -1
	closed   bool
	terminal bool
	inOff    int
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer {
	if cfg.WhitespaceLimit <= 0 {
		cfg.WhitespaceLimit = 1 << 30
	}
	return &Normalizer{cfg: cfg, pendOff: -1}
}

func (n *Normalizer) fail(err error, off int) error {
	n.terminal = true
	return &OffsetError{Err: err, Offset: off}
}

func (n *Normalizer) emitKeep(b byte, off int) error {
	if n.cfg.OutputLimit > 0 && len(n.out) >= n.cfg.OutputLimit {
		return n.fail(ErrOutputLimit, off)
	}
	n.out = append(n.out, b)
	n.tab.Keep(1)
	return nil
}

func (n *Normalizer) flushBuf() error {
	for _, b := range n.buf {
		if err := n.emitKeep(b, n.bufStart); err != nil {
			return err
		}
	}
	n.buf = n.buf[:0]
	return nil
}

func (n *Normalizer) dropBuf() {
	if len(n.buf) > 0 {
		n.tab.Delete(len(n.buf))
		n.buf = n.buf[:0]
	}
}

// Write feeds a chunk and returns the number of bytes consumed.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.terminal || n.closed {
		return 0, ErrClosed
	}
	for k, b := range p {
		n.inOff++
		off := n.inOff - 1
		if n.cfg.StrictNUL && b == 0 {
			return k, n.fail(ErrNUL, off)
		}
		for _, ev := range n.split.Feed(b) {
			if err := n.event(b, off, ev); err != nil {
				return k, err
			}
		}
	}
	return len(p), nil
}

func (n *Normalizer) event(b byte, off int, ev eol.Event) error {
	switch ev {
	case eol.PendingCR:
		n.pendOff = off
	case eol.LF:
		n.dropBuf()
		if b != '\n' {
			// Lone '\r': the '\r' itself supplies the output newline 1:1.
			n.pendOff = -1
			return n.emitKeep('\n', off)
		}
		if n.pendOff >= 0 {
			// "\r\n": '\r' deleted, '\n' kept.
			n.tab.Delete(1)
			n.pendOff = -1
		}
		return n.emitKeep('\n', off)
	case eol.Content:
		if ws.IsSpace(b) {
			if len(n.buf) == 0 {
				n.bufStart = off
			}
			if len(n.buf) >= n.cfg.WhitespaceLimit {
				return n.fail(ErrWhitespaceLimit, n.bufStart)
			}
			n.buf = append(n.buf, b)
			return nil
		}
		if err := n.flushBuf(); err != nil {
			return err
		}
		return n.emitKeep(b, off)
	}
	return nil
}

// Close finalizes the stream and applies the ending policy.
func (n *Normalizer) Close() error {
	if n.terminal {
		return ErrClosed
	}
	if n.closed {
		return nil
	}
	n.closed = true
	n.finishPending()
	n.dropBuf()
	return n.applyPolicy()
}

func (n *Normalizer) finishPending() {
	if ev := n.split.Flush(); len(ev) > 0 {
		n.dropBuf()
		// Trailing lone '\r' maps 1:1 to '\n'.
		_ = n.emitKeep('\n', n.pendOff)
		n.pendOff = -1
	}
}

// CloseInterior ends a non-final segment. A pending '\r' is forced to settle as
// a lone-CR line ending; buffered whitespace stays buffered (it is content that
// the next segment continues), then a sentinel forces it out and is removed.
func (n *Normalizer) CloseInterior() {
	if n.terminal {
		return
	}
	n.closed = true
	if n.split.Pending() {
		n.dropBuf()
		_ = n.emitKeep('\n', n.pendOff)
		n.pendOff = -1
		n.split.Force()
	}
	oBefore, uBefore := n.inOff, len(n.out)
	_ = n.flushBuf()
	n.inOff = oBefore + 1 // sentinel sits at a virtual offset
	_ = n.feedRaw(0x01)
	n.tab.Truncate(oBefore, uBefore)
	n.out = n.out[:uBefore]
	n.inOff = oBefore
}

func (n *Normalizer) feedRaw(b byte) error {
	if ws.IsSpace(b) || b == '\r' || b == '\n' {
		return nil
	}
	if err := n.flushBuf(); err != nil {
		return err
	}
	return n.emitKeep(b, n.inOff-1)
}

func (n *Normalizer) applyPolicy() error {
	switch n.cfg.Policy {
	case EnsureOne:
		if len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
			return n.appendNL()
		}
	case TrimEmpty:
		k := 0
		for k < len(n.out) && n.out[len(n.out)-1-k] == '\n' {
			k++
		}
		if len(n.out) == 0 {
			return nil
		}
		if k > 1 {
			n.trimOutput(k - 1)
		}
		if k == 0 {
			return n.appendNL()
		}
	}
	return nil
}

func (n *Normalizer) appendNL() error {
	if n.cfg.OutputLimit > 0 && len(n.out) >= n.cfg.OutputLimit {
		return n.fail(ErrOutputLimit, n.inOff)
	}
	n.out = append(n.out, '\n')
	n.tab.Insert(1)
	return nil
}

// trimOutput removes k trailing output bytes and clips the map to that point.
func (n *Normalizer) trimOutput(k int) {
	if k <= 0 {
		return
	}
	u := len(n.out) - k
	o := 0
	for _, r := range n.tab.Runs() {
		if r.U1 <= u {
			o = r.O1
			continue
		}
		if r.U0 <= u && r.O1-r.O0 == r.U1-r.U0 {
			o = r.O0 + (u - r.U0)
		}
		break
	}
	n.out = n.out[:u]
	n.tab.Truncate(o, u)
}

// Output returns a copy of the normalized bytes.
func (n *Normalizer) Output() []byte { return append([]byte(nil), n.out...) }

// Map returns the offset table (read-only use; do not mutate across writes).
func (n *Normalizer) Map() *span.Table { return &n.tab }
