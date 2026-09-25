// Package norm streams line ending and trailing-whitespace normalization.
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type Policy uint8

const (
	Keep Policy = iota
	EnsureOne
	TrimBlank
)

var (
	ErrNUL         = errors.New("norm: NUL byte")
	ErrWSLimit     = errors.New("norm: trailing whitespace buffer limit")
	ErrOutputLimit = errors.New("norm: output limit")
	ErrClosed      = errors.New("norm: normalizer is terminal")
)

type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

type Config struct {
	Policy      Policy
	StrictNUL   bool
	WSBuffer    int
	OutputLimit int
	Fragment    bool
}

type FragmentInfo struct {
	PendingCROffset int
	PendingWSStart  int
	PendingWSEnd    int
}

type Normalizer struct {
	cfg       Config
	out       []byte
	tab       span.Table
	pendingCR int
	run       ws.Run
	buf       []byte
	in, outN  int
	closed    bool
	err       error
	Fragment  FragmentInfo
}

func New(cfg Config) *Normalizer {
	if cfg.WSBuffer <= 0 {
		cfg.WSBuffer = 1 << 20
	}
	if cfg.OutputLimit <= 0 {
		cfg.OutputLimit = 1 << 30
	}
	return &Normalizer{cfg: cfg, pendingCR: -1}
}

func (n *Normalizer) fail(err error, offset int) error {
	n.closed, n.err = true, &OffsetError{err, offset}
	return n.err
}

func (n *Normalizer) Err() error                 { return n.err }
func (n *Normalizer) FragmentInfo() FragmentInfo { return n.Fragment }
func (n *Normalizer) Output() []byte {
	return append([]byte(nil), n.out...)
}
func (n *Normalizer) Map() *span.Table { return n.tab.Copy() }

func (n *Normalizer) emit(c byte, identityStart, origLen int) error {
	if n.outN+origLen > n.cfg.OutputLimit {
		return n.fail(ErrOutputLimit, n.in)
	}
	n.tab.Identity(identityStart, identityStart+origLen, n.outN, n.outN+origLen)
	n.out = append(n.out, c)
	n.outN += origLen
	return nil
}

func (n *Normalizer) flushRun() error {
	if !n.run.Valid() {
		return nil
	}
	for i, b := range n.buf {
		if err := n.emit(b, n.run.Start+i, 1); err != nil {
			return err
		}
	}
	n.run.Clear()
	n.buf = n.buf[:0]
	return nil
}

func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed {
		return 0, &OffsetError{ErrClosed, n.in}
	}
	for _, b := range p {
		off := n.in
		n.in++
		if b == 0 && n.cfg.StrictNUL {
			return 0, n.fail(ErrNUL, off)
		}
		kind := eol.Inspect(n.pendingCR >= 0, b)
		if n.pendingCR >= 0 && kind == eol.CRLF {
			start := n.pendingCR
			if n.run.Valid() {
				start = n.run.Start
				n.run.Clear()
				n.buf = n.buf[:0]
			}
			n.tab.Delete(start, off, n.outN)
			if err := n.emit('\n', off, 1); err != nil {
				return 0, err
			}
			n.pendingCR = -1
			continue
		}
		if n.pendingCR >= 0 {
			if err := n.emit('\n', n.pendingCR, 1); err != nil {
				return 0, err
			}
			n.pendingCR = -1
			kind = eol.Inspect(false, b)
		}
		switch kind {
		case eol.Other:
			if ws.IsTrailing(b) {
				if n.run.Len()+1 > n.cfg.WSBuffer && !n.cfg.Fragment {
					return 0, n.fail(ErrWSLimit, off)
				}
				n.run = n.run.Append(off)
				n.buf = append(n.buf, b)
				continue
			}
			if err := n.flushRun(); err != nil {
				return 0, err
			}
			if err := n.emit(b, off, 1); err != nil {
				return 0, err
			}
		case eol.LF:
			if n.run.Valid() {
				n.tab.Delete(n.run.Start, n.run.End, n.outN)
				n.run.Clear()
				n.buf = n.buf[:0]
			}
			if err := n.emit('\n', off, 1); err != nil {
				return 0, err
			}
		case eol.PendingCR:
			if n.run.Valid() {
				n.tab.Delete(n.run.Start, n.run.End, n.outN)
				n.run.Clear()
				n.buf = n.buf[:0]
			}
			n.pendingCR = off
		case eol.CRLF:
			n.pendingCR = off
		}
	}
	return len(p), nil
}

func (n *Normalizer) Close() error {
	if n.closed && !n.cfg.Fragment {
		return n.err
	}
	if n.pendingCR >= 0 {
		if n.cfg.Fragment {
			n.Fragment.PendingCROffset = n.pendingCR
		} else if err := n.emit('\n', n.pendingCR, 1); err != nil {
			return err
		}
	}
	if n.run.Valid() {
		if n.cfg.Fragment {
			n.Fragment.PendingWSStart, n.Fragment.PendingWSEnd = n.run.Start, n.run.End
		} else {
			n.tab.Delete(n.run.Start, n.run.End, n.outN)
			n.run.Clear()
			n.buf = n.buf[:0]
		}
	}
	if n.cfg.Fragment {
		n.closed = true
		return nil
	}
	n.finishPolicy()
	n.closed = true
	return n.err
}

func (n *Normalizer) finishPolicy() {
	if n.cfg.Policy == Keep || len(n.out) == 0 {
		return
	}
	trailing := 0
	for trailing < len(n.out) && n.out[len(n.out)-1-trailing] == '\n' {
		trailing++
	}
	if trailing <= 1 {
		return
	}
	origEnd := n.tab.ToOrig(len(n.out))
	deleteStart := n.tab.ToOrig(len(n.out) - trailing + 1)
	n.out = append([]byte(nil), n.out[:len(n.out)-trailing+1]...)
	n.outN = len(n.out)
	n.tab.TrimEnd(len(n.out), deleteStart, origEnd)
}
