// Package patch applies unified diffs to text with fuzzed positional lookup,
// atomic rejection, reverse application and a versioned multi-document store.
package patch

import (
	"errors"

	"ontology/edit"
	"ontology/lines"
	"ontology/udiff"
)

// Failure categories.
var (
	ErrFormat      = udiff.ErrFormat
	ErrContext     = errors.New("patch: hunk context does not match")
	ErrOffset      = errors.New("patch: hunk only matches outside the offset range")
	ErrTooDifferent = edit.ErrTooDifferent
)

// ApplyError identifies the failing hunk (0-based) and its category.
type ApplyError struct {
	Hunk int
	Err  error
}

func (e *ApplyError) Error() string { return e.Err.Error() }
func (e *ApplyError) Unwrap() error { return e.Err }

// Options control application. F is the +/- row fuzz window.
type Options struct {
	F      int
	Limits udiff.Limits
}

// Apply parses text and applies it to data. Any failing hunk leaves data
// byte-for-byte unchanged and returns *ApplyError.
func Apply(data, text []byte, opt Options) ([]byte, error) {
	p, err := udiff.Parse(text, opt.Limits)
	if err != nil {
		return nil, err
	}
	cur := lines.Split(data)
	out, err := applyAll(cur, p, opt.F)
	if err != nil {
		return nil, err
	}
	return lines.Join(out), nil
}

// Reverse applies the patch backwards, transforming b into a.
func Reverse(data, text []byte, opt Options) ([]byte, error) {
	p, err := udiff.Parse(text, opt.Limits)
	if err != nil {
		return nil, err
	}
	cur := lines.Split(data)
	out, err := applyAll(cur, reversePatch(p), opt.F)
	if err != nil {
		return nil, err
	}
	return lines.Join(out), nil
}

func applyAll(cur []lines.Line, p *udiff.Patch, f int) ([]lines.Line, error) {
	shift := 0
	for hi, h := range p.Hunks {
		rec := h.OldStart - 1
		if h.OldCount == 0 {
			rec = h.OldStart
		}
		center := rec + shift
		idx, ok := locate(cur, h, center, f)
		if !ok {
			return nil, &ApplyError{Hunk: hi, Err: classify(cur, h, center, f)}
		}
		cur = splice(cur, idx, h)
		shift = idx - rec
	}
	return cur, nil
}

// locate returns the nearest exact match of the old-side block to center within
// +/-f; ties prefer the earlier position.
func locate(cur []lines.Line, h udiff.Hunk, center, f int) (int, bool) {
	pat := oldBlock(h)
	lo, hi := bounds(len(cur), len(pat), center, f)
	best, found := 0, false
	for pos := lo; pos <= hi; pos++ {
		if matchAt(cur, pat, pos) && (!found || abs(pos-center) < abs(best-center)) {
			best, found = pos, true
		}
	}
	return best, found
}

func classify(cur []lines.Line, h udiff.Hunk, center, f int) error {
	pat := oldBlock(h)
	lo, hi := 0, len(cur)-len(pat)
	if len(pat) == 0 {
		hi = len(cur)
	}
	for pos := lo; pos <= hi; pos++ {
		if matchAt(cur, pat, pos) && abs(pos-center) > f {
			return ErrOffset
		}
	}
	return ErrContext
}

func bounds(n, plen, center, f int) (int, int) {
	lo, hi := center-f, center+f
	if lo < 0 {
		lo = 0
	}
	max := n - plen
	if hi > max {
		hi = max
	}
	return lo, hi
}

type seg struct {
	text string
	nl   []byte
}

func oldBlock(h udiff.Hunk) []seg {
	out := []seg{}
	for i, l := range h.Lines {
		if l.Kind == udiff.New {
			continue
		}
		out = append(out, seg{l.Text, nlFor(h.OldNoNL, i, l.CR)})
	}
	return out
}

func nlFor(noNL []int, i int, cr bool) []byte {
	for _, x := range noNL {
		if x == i {
			return nil
		}
	}
	if cr {
		return []byte("\r\n")
	}
	return []byte("\n")
}

func matchAt(cur []lines.Line, pat []seg, pos int) bool {
	if pos < 0 || pos+len(pat) > len(cur) {
		return false
	}
	for i, s := range pat {
		if string(cur[pos+i].Text) != s.text || string(cur[pos+i].NL) != string(s.nl) {
			return false
		}
	}
	return true
}

func splice(cur []lines.Line, at int, h udiff.Hunk) []lines.Line {
	next := make([]lines.Line, 0, len(cur)-h.OldCount+h.NewCount)
	next = append(next, cur[:at]...)
	for i, l := range h.Lines {
		if l.Kind == udiff.Old {
			continue
		}
		next = append(next, lines.Line{Text: []byte(l.Text), NL: nlFor(h.NewNoNL, i, l.CR)})
	}
	next = append(next, cur[at+h.OldCount:]...)
	return next
}

func reversePatch(p *udiff.Patch) *udiff.Patch {
	r := &udiff.Patch{}
	for _, h := range p.Hunks {
		rh := udiff.Hunk{
			OldStart: h.NewStart, OldCount: h.NewCount,
			NewStart: h.OldStart, NewCount: h.OldCount,
			OldNoNL: h.NewNoNL, NewNoNL: h.OldNoNL,
		}
		for _, l := range h.Lines {
			switch l.Kind {
			case udiff.Old:
				l.Kind = udiff.New
			case udiff.New:
				l.Kind = udiff.Old
			}
			rh.Lines = append(rh.Lines, l)
		}
		r.Hunks = append(r.Hunks, rh)
	}
	return r
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
