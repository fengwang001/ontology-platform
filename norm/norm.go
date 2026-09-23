// Package norm is a streaming normalizer of line endings and trailing
// whitespace with a bidirectional offset map. A single Writer is not safe
// for concurrent use; multiple independent Writers are.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Trailing selects the end-of-stream newline policy.
type Trailing int

const (
	Keep      Trailing = iota // leave the tail untouched
	EnsureOne                 // non-empty output ends with exactly one \n
	Trim                      // drop trailing blank lines, keep one \n
)

// Config configures a Writer.
type Config struct {
	Trailing     Trailing
	Strict       bool // reject NUL bytes
	MaxPendingWS int  // undecided whitespace cap; <=0 => 1 MiB
	MaxOutput    int  // output byte cap; <=0 => unlimited
	OrigBase     int  // added to original offsets (used by par)
	OutBase      int  // added to output offsets (used by par)
}

var (
	// ErrClosed is returned after a terminal error or close.
	ErrClosed = errors.New("norm: writer closed")
	// ErrNUL is a strict-mode NUL failure.
	ErrNUL = errors.New("norm: NUL byte")
	// ErrPendingOverflow is an undecided-whitespace overflow.
	ErrPendingOverflow = errors.New("norm: pending whitespace overflow")
	// ErrOutputLimit is an output-size overflow.
	ErrOutputLimit = errors.New("norm: output limit exceeded")
)

// OffsetError wraps a sentinel with the confirmed original offset.
type OffsetError struct {
	Err        error
	OrigOffset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Writer normalizes a byte stream.
type Writer struct {
	cfg      Config
	det      eol.Detector
	pend     ws.Pending
	out      []byte
	seg      []span.Segment
	consumed int
	pos      int
	closed   bool
	err      error
}

// New creates a Writer.
func New(cfg Config) *Writer {
	if cfg.MaxPendingWS <= 0 {
		cfg.MaxPendingWS = 1 << 20
	}
	return &Writer{cfg: cfg}
}

func id(s span.Segment) bool { return s.O1-s.O0 == s.P1-s.P0 }

func (w *Writer) add(s span.Segment) {
	if n := len(w.seg); n > 0 {
		a := w.seg[n-1]
		if a.O1 == s.O0 && a.P1 == s.P0 && !id(a) == !id(s) && id(a) {
			w.seg[n-1].O1, w.seg[n-1].P1 = s.O1, s.P1
			return
		}
	}
	w.seg = append(w.seg, s)
}

func (w *Writer) fail(err error, p int) error {
	w.err = &OffsetError{Err: err, OrigOffset: w.cfg.OrigBase + p}
	w.closed = true
	return w.err
}

func (w *Writer) keep(p int, b byte) error {
	if w.cfg.MaxOutput > 0 && w.consumed >= w.cfg.MaxOutput {
		return w.fail(ErrOutputLimit, p)
	}
	w.out = append(w.out, b)
	w.add(span.Segment{p, p + 1, w.consumed, w.consumed + 1})
	w.consumed++
	return nil
}

func (w *Writer) drop(p int) { w.add(span.Segment{p, p + 1, w.consumed, w.consumed}) }

func (w *Writer) emitNL() error {
	if w.cfg.MaxOutput > 0 && w.consumed >= w.cfg.MaxOutput {
		return w.fail(ErrOutputLimit, w.pos)
	}
	w.out = append(w.out, '\n')
	w.consumed++
	return nil
}

func (w *Writer) endLine(crPos int) error {
	start := w.pos - w.pend.Len()
	if crPos >= 0 {
		start = crPos - w.pend.Len()
	}
	for i := range w.pend.Take() {
		w.drop(start + i)
	}
	if crPos >= 0 {
		w.drop(crPos)
	}
	return w.emitNL()
}

func (w *Writer) flushPend() error {
	start := w.pos - w.pend.Len()
	for i, b := range w.pend.Take() {
		if err := w.keep(start+i, b); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) byte(b byte) error {
	if w.cfg.Strict && b == 0 {
		return w.fail(ErrNUL, w.pos)
	}
	wasPending := w.det.Pending()
	switch w.det.Feed(b) {
	case eol.LF:
		cr := -1
		if wasPending {
			cr = w.pos - 1
		}
		return w.endLine(cr)
	case eol.CR:
		return w.endLine(w.pos - 1)
	case eol.ResumePending:
		if err := w.flushPend(); err != nil {
			return err
		}
		if err := w.keep(w.pos-1, '\r'); err != nil {
			return err
		}
	case eol.PendingCR:
		if err := w.flushPend(); err != nil {
			return err
		}
	case eol.Content:
	}
	if ws.IsSpace(b) {
		if w.pend.Len() >= w.cfg.MaxPendingWS {
			return w.fail(ErrPendingOverflow, w.pos)
		}
		w.pend.Add(b)
		return nil
	}
	// A pending \r is resolved below when its successor is known.
	if b == '\r' {
		return nil
	}
	if err := w.flushPend(); err != nil {
		return err
	}
	return nil
}

// Write feeds one chunk.
func (w *Writer) Write(data []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	for i := 0; i < len(data); i++ {
		if err := w.byte(data[i]); err != nil {
			w.closed = true
			return i, err
		}
		w.pos++
	}
	return len(data), nil
}

// Close resolves pending state and applies the trailing policy.
func (w *Writer) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.det.Pending() {
		if err := w.endLine(w.pos - 1); err != nil {
			return err
		}
	} else if n := w.pend.Len(); n > 0 {
		for i := range w.pend.Take() {
			w.drop(w.pos - n + i)
		}
	}
	switch w.cfg.Trailing {
	case EnsureOne:
		if w.consumed > 0 && w.out[w.consumed-1] != '\n' {
			if err := w.emitNL(); err != nil {
				return err
			}
			w.add(span.Segment{w.pos, w.pos, w.consumed - 1, w.consumed})
		}
	case Trim:
		k := 0
		for k < w.consumed && w.out[w.consumed-1-k] == '\n' {
			k++
		}
		if k > 1 {
			cut := w.consumed - k + 1
			w.out = append(w.out[:cut], '\n')
			w.consumed = cut + 1
			for len(w.seg) > 0 && w.seg[len(w.seg)-1].P0 >= cut {
				w.seg = w.seg[:len(w.seg)-1]
			}
			if n := len(w.seg); n > 0 && w.seg[n-1].P1 > cut {
				w.seg[n-1].P1 = cut
			}
			w.add(span.Segment{w.pos, w.pos, cut, cut + 1})
		}
	}
	return nil
}

// Output returns normalized bytes (valid after Close).
func (w *Writer) Output() []byte { return w.out }

// Map returns the offset map with configured coordinate bases applied.
func (w *Writer) Map() *span.Map {
	var b span.Builder
	for _, s := range w.seg {
		b.Add(span.Segment{
			s.O0 + w.cfg.OrigBase, s.O1 + w.cfg.OrigBase,
			s.P0 + w.cfg.OutBase, s.P1 + w.cfg.OutBase,
		})
	}
	return b.Build()
}

// Normalize is a one-shot helper.
func Normalize(data []byte, cfg Config) ([]byte, *span.Map, error) {
	w := New(cfg)
	if _, err := w.Write(data); err != nil {
		return w.Output(), w.Map(), err
	}
	err := w.Close()
	return w.Output(), w.Map(), err
}
