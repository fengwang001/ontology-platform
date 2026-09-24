// Package norm is a streaming line-ending and trailing-whitespace
// normalizer with a bidirectional original/output offset map.
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
	Keep  Policy = iota // keep all trailing newlines
	One                 // at most one trailing newline
	Blank               // drop trailing blank lines, keep one if any newline
)

// Sentinel errors.
var (
	ErrNUL          = errors.New("norm: NUL byte in strict mode")
	ErrWSOverflow   = errors.New("norm: pending whitespace limit exceeded")
	ErrOutputLimit  = errors.New("norm: output size limit exceeded")
	ErrAfterTerminal = errors.New("norm: write after terminal state")
)

// OffsetError attaches the original byte offset to a sentinel error.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Config configures a Normalizer. Zero limits are unlimited.
type Config struct {
	End          Policy
	WSLimit      int
	OutputLimit  int
	StrictNUL    bool
}

// Normalizer is a single-writer streaming normalizer; it is not safe for
// concurrent use. Output is available after Close.
type Normalizer struct {
	cfg     Config
	dec     eol.Decoder
	tracker *ws.Tracker
	out     []byte
	mp      *span.Map
	origPos int
	// crAt is the original offset of an unresolved '\r', or -1.
	pendingCRAt int
	// wsAfterCR marks buffered whitespace that follows that '\r'.
	wsAfterCR bool
	wsStart     int
	closed      bool
	failed      error
}

// New returns a Normalizer.
func New(cfg Config) *Normalizer {
	return &Normalizer{
		cfg:         cfg,
		tracker:     ws.New(cfg.WSLimit),
		mp:          span.New(0),
		pendingCRAt: -1,
	}
}

// Write feeds one chunk. After a terminal error the normalizer stays
// readable but further writes fail with ErrAfterTerminal.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.failed != nil || n.closed {
		return 0, &OffsetError{Err: ErrAfterTerminal, Offset: n.origPos}
	}
	for i := 0; i < len(p); i++ {
		off := n.origPos
		b := p[i]
		if n.cfg.StrictNUL && b == 0 {
			n.fail(ErrNUL, off)
			return i, n.failed
		}
		ev := n.dec.Feed(b)
		switch ev {
		case eol.LF:
			n.finishLine(off)
			if n.failed != nil {
				return i, n.failed
			}
		case eol.CR:
			// Prior '\r' was a lone line ending and the current byte is
			// the next '\r', which is now itself pending.
			n.resolveLoneCR(off, n.wsAfterCR)
			if n.failed != nil {
				return i, n.failed
			}
			n.pendingCRAt = off
			n.wsAfterCR = false
		case eol.Pending:
			n.flushKept()
			if n.failed != nil {
				return i, n.failed
			}
			n.pendingCRAt = off
			n.wsAfterCR = false
		case eol.Other:
			if n.pendingCRAt >= 0 {
				n.resolveLoneCR(off, n.wsAfterCR)
				n.pendingCRAt = -1
				n.wsAfterCR = false
				if n.failed != nil {
					return i, n.failed
				}
			}
			n.feedOther(b, off)
			if n.failed != nil {
				return i, n.failed
			}
		}
		n.origPos++
	}
	return len(p), nil
}

// feedOther handles a non-line-ending byte.
func (n *Normalizer) feedOther(b byte, off int) {
	if !ws.IsWS(b) {
		n.flushKept()
		n.keep(b, off)
		return
	}
	if n.tracker.Len() == 0 {
		n.wsStart = off
		if n.pendingCRAt >= 0 {
			n.wsAfterCR = true
		}
	}
	if n.tracker.Add(b) {
		return
	}
	n.fail(ErrWSOverflow, off)
}

// keep appends an ordinary retained byte.
func (n *Normalizer) keep(b byte, off int) {
	if !n.reserve(1, off) {
		return
	}
	n.out = append(n.out, b)
}

// flushKept emits a buffered whitespace run that proved to be mid-line.
func (n *Normalizer) flushKept() {
	b := n.tracker.Keep()
	if len(b) == 0 {
		return
	}
	if !n.reserve(len(b), n.wsStart) {
		return
	}
	n.out = append(n.out, b...)
}

// resolveLoneCR emits the pending '\r' as a retained '\n'. When paired
// is false the '\r' is lone; any whitespace between the '\r' and the
// next byte only counts as trailing when it directly followed the '\r'.
func (n *Normalizer) resolveLoneCR(nextOff int, wsAfterCR bool) {
	if wsAfterCR && n.tracker.Len() > 0 {
		wsBeg := n.pendingCRAt + 1
		n.mp.Delete(wsBeg, nextOff, len(n.out))
		n.tracker.Drop()
	} else {
		n.flushKept()
		if n.failed != nil {
			return
		}
	}
	if !n.reserve(1, n.pendingCRAt) {
		return
	}
	n.out = append(n.out, '\n')
}

// finishLine consumes a '\n' at nlOff, deleting buffered trailing
// whitespace and the paired '\r' when the sequence was "\r\n".
func (n *Normalizer) finishLine(nlOff int) {
	wsLen := n.tracker.Len()
	wsBeg := nlOff - wsLen
	crlf := n.pendingCRAt >= 0
	delBeg := wsBeg
	if crlf {
		if n.wsAfterCR {
			wsBeg = n.pendingCRAt + 1
		}
		delBeg = n.pendingCRAt
	}
	if delBeg < nlOff {
		n.mp.Delete(delBeg, nlOff, len(n.out))
	}
	n.tracker.Drop()
	n.pendingCRAt = -1
	if !n.reserve(1, nlOff) {
		return
	}
	n.out = append(n.out, '\n')
}

// reserve enforces the output limit before appending bytes.
func (n *Normalizer) reserve(add, off int) bool {
	if n.cfg.OutputLimit > 0 && len(n.out)+add > n.cfg.OutputLimit {
		n.fail(ErrOutputLimit, off)
		return false
	}
	return true
}

func (n *Normalizer) fail(err error, off int) {
	n.failed = &OffsetError{Err: err, Offset: off}
}

// Close finalizes the stream: trailing whitespace is deleted, a lone
// pending '\r' becomes '\n', and the end policy is applied. Close on an
// already terminal normalizer returns the terminal error.
func (n *Normalizer) Close() error {
	if n.failed != nil {
		return n.failed
	}
	if n.closed {
		return &OffsetError{Err: ErrAfterTerminal, Offset: n.origPos}
	}
	if n.pendingCRAt >= 0 {
		n.resolvePendingCRAtEOF()
		n.pendingCRAt = -1
		if n.failed != nil {
			return n.failed
		}
	}
	if n.tracker.Len() > 0 {
		n.mp.Delete(n.wsStart, n.wsStart+n.tracker.Len(), len(n.out))
		n.tracker.Drop()
	}
	n.applyPolicy()
	n.closed = true
	return nil
}

// resolvePendingCRAtEOF turns a trailing lone '\r' into '\n'; whitespace
// buffered before it is trailing whitespace and is deleted.
func (n *Normalizer) resolvePendingCRAtEOF() {
	if n.tracker.Len() > 0 {
		wsEnd := n.pendingCRAt
		wsBeg := wsEnd - n.tracker.Len()
		n.mp.Delete(wsBeg, wsEnd, len(n.out))
		n.tracker.Drop()
	}
	if !n.reserve(1, n.pendingCRAt) {
		return
	}
	n.out = append(n.out, '\n')
}

// applyPolicy trims trailing '\n' bytes per the configured policy.
func (n *Normalizer) applyPolicy() {
	end := len(n.out)
	k := 0
	for k < end && n.out[end-1-k] == '\n' {
		k++
	}
	keep := 0
	switch n.cfg.End {
	case Keep:
		keep = k
	case One:
		if k > 0 {
			keep = 1
		}
	case Blank:
		if k > 0 && k < end {
			keep = 1
		}
	}
	if keep == k {
		return
	}
	newEnd := end - (k - keep)
	origCut := n.mp.ToOrig(newEnd)
	origEnd := n.mp.ToOrig(end)
	n.out = n.out[:newEnd]
	if origEnd > origCut {
		n.mp.ReplaceSuffix(origCut, origEnd, newEnd)
	}
}

// Output returns the normalized bytes (valid after Close).
func (n *Normalizer) Output() []byte { return n.out }

// OrigLen returns the number of original bytes consumed.
func (n *Normalizer) OrigLen() int { return n.origPos }

// MapToOrig maps an output offset to an original offset.
func (n *Normalizer) MapToOrig(o int) int { return n.mp.ToOrig(o) }

// MapToOut maps an original offset to an output offset.
func (n *Normalizer) MapToOut(i int) int { return n.mp.ToOut(i) }

// Checked returns intervals inspected by the latest map query.
func (n *Normalizer) Checked() int { return n.mp.Checked() }

// SpanCount returns the number of mapped intervals.
func (n *Normalizer) SpanCount() int { return n.mp.Count() }
