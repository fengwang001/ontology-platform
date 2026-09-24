// Package patch applies parsed unified diffs to in-memory text, including
// fuzzy offset lookup, atomic rejection and reverse application.
package patch

import (
	"errors"
	"fmt"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// Reason categorizes an application failure.
type Reason int

// Distinct failure categories (four, together with edit.ErrTooDifferent).
const (
	FormatReason Reason = iota + 1
	ContextMismatch
	OutOfRange
)

// Error is the typed, classifiable application/parse failure.
type Error struct {
	Reason Reason
	Hunk   int // 1-based hunk index; 0 when not hunk-specific
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("patch: hunk %d: %s", e.Hunk, e.Err)
}
func (e *Error) Unwrap() error { return e.Err }

var (
	errContext = errors.New("context and removed lines do not match")
	errRange   = errors.New("no matching location within fuzz range")
)

// IsContext reports a context mismatch.
func IsContext(e error) bool { var t *Error; return errors.As(e, &t) && t.Reason == ContextMismatch }

// IsOutOfRange reports failure because no candidate lay within ±F.
func IsOutOfRange(e error) bool { var t *Error; return errors.As(e, &t) && t.Reason == OutOfRange }

// IsFormat reports a malformed patch.
func IsFormat(e error) bool {
	var t *Error
	if errors.As(e, &t) {
		return t.Reason == FormatReason
	}
	var f *udiff.FormatError
	return errors.As(e, &f)
}

// Options controls application.
type Options struct {
	Fuzz int // search radius in lines on each side of the recorded point
}

func toLine(r hunk.Row) lines.Line {
	l := append(lines.Line{}, r.Text...)
	if r.HasNL {
		if r.CRLF {
			l = append(l, '\r')
		}
		l = append(l, '\n')
	}
	return l
}

func applyHunk(src []lines.Line, hh udiff.Hunk, anchor int, fuzz int) ([]lines.Line, int, error) {
	var oldSeg, newSeg []lines.Line
	for _, r := range hh.Rows {
		switch r.Op {
		case ' ':
			l := toLine(r)
			oldSeg = append(oldSeg, l)
			newSeg = append(newSeg, l)
		case '-':
			oldSeg = append(oldSeg, toLine(r))
		case '+':
			newSeg = append(newSeg, toLine(r))
		}
	}
	c := anchor - 1
	lo, hi := c-fuzz, c+fuzz
	if lo < 0 {
		lo = 0
	}
	if hi > len(src) {
		hi = len(src)
	}
	best, bestDist := -1, 0
	for p := lo; p <= hi && p+len(oldSeg) <= len(src); p++ {
		if !segEqual(src[p:p+len(oldSeg)], oldSeg) {
			continue
		}
		d := p - c
		if d < 0 {
			d = -d
		}
		if best < 0 || d < bestDist || d == bestDist && p < best {
			best, bestDist = p, d
		}
	}
	if best < 0 {
		reason := OutOfRange
		if inRangeMatch(src, lo, hi, oldSeg) {
			reason = ContextMismatch
		}
		if reason == ContextMismatch {
			return nil, 0, &Error{Reason: reason, Err: errContext}
		}
		return nil, 0, &Error{Reason: reason, Err: errRange}
	}
	out := make([]lines.Line, 0, len(src)-len(oldSeg)+len(newSeg))
	out = append(out, src[:best]...)
	out = append(out, newSeg...)
	out = append(out, src[best+len(oldSeg):]...)
	delta := best - c
	return out, delta, nil
}

func inRangeMatch(src []lines.Line, lo, hi int, seg []lines.Line) bool {
	for p := lo; p <= hi && p+len(seg) <= len(src); p++ {
		if segEqual(src[p:p+len(seg)], seg) {
			return true
		}
	}
	return false
}

func segEqual(a, b []lines.Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !lines.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
