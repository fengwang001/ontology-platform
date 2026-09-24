// Package patch applies parsed unified diffs to text, with fuzz offsets,
// atomic rejection, reversal, and a versioned multi-document store.
package patch

import (
	"errors"
	"fmt"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// Reason categorizes an application failure.
type Reason uint8

const (
	// RFormat is a malformed patch.
	RFormat Reason = iota + 1
	// RContext means the hunk context/deletions matched nowhere.
	RContext
	// ROffset means the nearest match was beyond Fuzz lines.
	ROffset
)

// Error reports which hunk failed and why.
type Error struct {
	Hunk   int // 1-based hunk index
	Reason Reason
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("patch hunk %d: %s", e.Hunk, e.Msg)
}

// Is reports sentinel categories without importing them directly.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrContext:
		return e.Reason == RContext
	case ErrOffset:
		return e.Reason == ROffset
	case ErrFormat:
		return e.Reason == RFormat
	}
	return false
}

// Sentinel categories for errors.Is.
var (
	ErrFormat  = errors.New("patch: malformed patch")
	ErrContext = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: offset out of range")
)

// Options tune application.
type Options struct {
	Fuzz   int // search ±Fuzz lines around the recorded position; 0 = exact
	Limits udiff.Limits
}

// Apply parses text and applies it to a. On any hunk failure a is left
// untouched and a *Error is returned.
func Apply(a, text []byte, opt Options) ([]byte, error) {
	p, err := udiff.Parse(text, opt.Limits)
	if err != nil {
		return nil, &Error{Reason: reasonOf(err), Msg: err.Error()}
	}
	cur := lines.Split(a)
	shift := 0
	for hi, h := range p.Hunks {
		pos, delta, cause := locate(cur, h, h.OldStart, shift, opt.Fuzz)
		if cause != 0 {
			return nil, &Error{Hunk: hi + 1, Reason: cause,
				Msg: "no matching context within fuzz range"}
		}
		cur = splice(cur, pos, h)
		shift += delta
	}
	return lines.Join(cur), nil
}

// Reverse returns the reverse patch text (swap headers and -/+ rows).
func Reverse(text []byte, lim udiff.Limits) ([]byte, error) {
	p, err := udiff.Parse(text, lim)
	if err != nil {
		return nil, err
	}
	rp := udiff.Patch{OldName: p.NewName, NewName: p.OldName}
	for _, h := range p.Hunks {
		rh := hunk.Hunk{
			OldStart: h.NewStart, OldCount: h.NewCount,
			NewStart: h.OldStart, NewCount: h.OldCount,
		}
		for _, r := range h.Rows {
			rr := hunk.Row{Line: r.Line, OldNoNL: r.NewNoNL, NewNoNL: r.OldNoNL}
			switch r.Kind {
			case hunk.Old:
				rr.Kind = hunk.New
			case hunk.New:
				rr.Kind = hunk.Old
			default:
				rr.Kind = r.Kind
			}
			rh.Rows = append(rh.Rows, rr)
		}
		rp.Hunks = append(rp.Hunks, rh)
	}
	return rp.Render(), nil
}

func reasonOf(err error) Reason {
	return RFormat
}
