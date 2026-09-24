// Package hunk groups an edit script into context-bounded hunks.
package hunk

import "ontology/edit"

// Hunk is a group of changes plus surrounding context lines, with the
// header line numbers of both sides (1-based; see DESIGN.md for the
// zero-count rule).
type Hunk struct {
	Ops                                    []edit.Op
	OldStart, OldCount, NewStart, NewCount int
}

// Diff computes the hunks between a and b with ctx context lines.
func Diff(a, b []byte, ctx, limit int) ([]Hunk, error) {
	ops, err := edit.Diff(a, b, limit)
	if err != nil {
		return nil, err
	}
	return Group(ops, ctx), nil
}

// Group splits ops into hunks with ctx context lines. Changes separated
// by at most 2*ctx unchanged lines merge into a single hunk.
func Group(ops []edit.Op, ctx int) []Hunk {
	var ch []int
	for i, op := range ops {
		if op.Kind != ' ' {
			ch = append(ch, i)
		}
	}
	if len(ch) == 0 {
		return nil
	}
	var hs []Hunk
	lo, hi := ch[0], ch[0]
	for _, i := range ch[1:] {
		if i-hi-1 > 2*ctx {
			hs = append(hs, build(ops, lo, hi, ctx))
			lo = i
		}
		hi = i
	}
	return append(hs, build(ops, lo, hi, ctx))
}

func build(ops []edit.Op, lo, hi, ctx int) Hunk {
	from := max(lo-ctx, 0)
	to := min(hi+ctx, len(ops)-1)
	var ob, nb int
	for _, op := range ops[:from] {
		if op.Kind != '+' {
			ob++
		}
		if op.Kind != '-' {
			nb++
		}
	}
	h := Hunk{Ops: ops[from : to+1], OldStart: ob, NewStart: nb}
	for _, op := range h.Ops {
		if op.Kind != '+' {
			h.OldCount++
		}
		if op.Kind != '-' {
			h.NewCount++
		}
	}
	if h.OldCount > 0 {
		h.OldStart++
	}
	if h.NewCount > 0 {
		h.NewStart++
	}
	return h
}
