// Package patch applies unified diffs to text with offset search, atomic
// rejection, reverse application, and a concurrent multi-document store.
package patch

import (
	"errors"
	"fmt"

	"ontology/edit"
	"ontology/lines"
	"ontology/udiff"
)

// Sentinel error categories; all four spec categories stay distinguishable.
var (
	ErrMalformed        = udiff.ErrMalformed
	ErrContextMismatch  = errors.New("patch: context does not match")
	ErrOffsetExceeded   = errors.New("patch: no match within offset range")
	ErrDistanceExceeded = edit.ErrDistanceExceeded
)

// HunkError identifies the failing hunk (0-based) and the reason category.
type HunkError struct {
	Hunk  int
	Cause error
}

func (e *HunkError) Error() string {
	return fmt.Sprintf("patch: hunk %d failed: %v", e.Hunk, e.Cause)
}
func (e *HunkError) Unwrap() error { return e.Cause }

// Options configure application.
type Options struct {
	Fuzz   int // search ±Fuzz lines around the recorded position
	Limits udiff.Limits
}

// DefaultOptions search 3 lines each way.
func DefaultOptions() Options { return Options{Fuzz: 3} }

type sideLine struct {
	line lines.Line
	nonl bool
}

func sides(h udiff.Hunk) (oldS, newS []sideLine, oldNonl, newNonl bool) {
	for i, ln := range h.Lines {
		if ln.Kind != edit.Insert {
			oldS = append(oldS, sideLine{line: ln.Line, nonl: ln.NoNL})
			if i == len(h.Lines)-1 {
				oldNonl = ln.NoNL
			}
		}
		if ln.Kind != edit.Delete {
			newS = append(newS, sideLine{line: ln.Line, nonl: ln.NoNL})
			if i == len(h.Lines)-1 {
				newNonl = ln.NoNL
			}
		}
}
	return
}

func center(h udiff.Hunk) int {
	if h.OldN == 0 {
		return h.OldStart // 0-based insertion index
	}
	return h.OldStart - 1
}

func fits(t []lines.Line, pos int, want []sideLine, nonl bool) bool {
	if pos < 0 || pos+len(want) > len(t) {
		return false
	}
	for i, w := range want {
		tl := t[pos+i]
		if !lines.Equal(tl, w.line) {
			return false
		}
	}
	if nonl {
		last := pos + len(want) - 1
		if last != len(t)-1 || t[last].NL() {
			return false
		}
	}
	return true
}

func applyHunks(t []lines.Line, hs []udiff.Hunk, fuzz int) ([]lines.Line, error) {
	shift := 0
	for hi, h := range hs {
		oldS, newS, oldNonl, newNonl := sides(h)
		c := center(h) + shift
		best, bestDist := -1, 0
		for d := 0; d <= fuzz; d++ {
			for _, cand := range []int{c - d, c + d} {
				if d == 0 && cand == c {
					cand = c
				}
				if fits(t, cand, oldS, oldNonl) {
					best, bestDist = cand, d
					goto found
				}
			}
		}
		if best < 0 {
			if fits(t, c, oldS, oldNonl) {
				best, bestDist = c, 0
			}
		}
	found:
		if best < 0 {
			cause := ErrOffsetExceeded
			if fuzz == 0 {
				cause = ErrContextMismatch
			}
			return nil, &HunkError{Hunk: hi, Cause: cause}
		}
		_ = bestDist
		out := make([]lines.Line, 0, len(t)+len(newS)-len(oldS))
		out = append(out, t[:best]...)
		for j, w := range newS {
			l := w.line
			if newNonl && j == len(newS)-1 {
				l.End = ""
			}
			out = append(out, l)
		}
		out = append(out, t[best+len(oldS):]...)
		shift += len(newS) - len(oldS)
		t = out
	}
	return t, nil
}

// Apply parses and applies text to target. Any hunk failure leaves target
// bytes unchanged (work happens on a copy until all hunks succeed).
func Apply(target []byte, text []byte, opt Options) ([]byte, error) {
	return applySide(target, text, opt, false)
}

// Reverse applies the patch backwards.
func Reverse(target []byte, text []byte, opt Options) ([]byte, error) {
	return applySide(target, text, opt, true)
}

func applySide(target, text []byte, opt Options, rev bool) ([]byte, error) {
	p, err := udiff.Parse(text, opt.Limits)
	if err != nil {
		return nil, err
	}
	hs := p.Hunks
	if rev {
		hs = make([]udiff.Hunk, len(p.Hunks))
		for i, h := range p.Hunks {
			h.OldStart, h.NewStart = h.NewStart, h.OldStart
			h.OldN, h.NewN = h.NewN, h.OldN
			for j, ln := range h.Lines {
				switch ln.Kind {
				case edit.Delete:
					ln.Kind = edit.Insert
				case edit.Insert:
					ln.Kind = edit.Delete
				}
				h.Lines[j] = ln
			}
			hs[i] = h
		}
	}
	t := lines.Split(target)
	out, err := applyHunks(t, hs, opt.Fuzz)
	if err != nil {
		return nil, err
	}
	return lines.Join(out), nil
}
