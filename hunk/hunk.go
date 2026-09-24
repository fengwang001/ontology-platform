// Package hunk groups shortest edit scripts into contextual unified-diff hunks.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Kind is the prefix character of a rendered body row.
type Kind uint8

const (
	Ctx  Kind = iota // unchanged " "
	Old              // removed "-"
	New              // added "+"
	Mark             // "\ No newline at end of file"
)

// Row is one body line of a hunk. Mark rows carry an empty Line.
type Row struct {
	Kind             Kind
	Line             lines.Line
	OldNoNL, NewNoNL bool
}

// Hunk is one @@ block. For a zero count, Start is the 1-based line before
// the insertion point (0 at file start), matching GNU diff.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

type frow struct {
	kind             Kind
	line             lines.Line
	ao, an, bo, bn   int // -1 if absent on that side
	oldMark, newMark bool
}

// Build groups segs into hunks with up to C context lines. Two changes
// separated by g unchanged lines merge when g <= 2*C, split at g == 2*C+1.
func Build(segs []edit.Segment, C int) []Hunk {
	type chg struct{ a0, a1, b0, b1 int }
	var (
		fr      []frow
		changes []chg
		ai, bi  int
	)
	for _, s := range segs {
		for i, ln := range s.Lines {
			r := frow{ao: -1, an: -1, bo: -1, bn: -1}
			last := i == len(s.Lines)-1
			switch s.Op {
			case edit.Equal:
				r.kind, r.ao, r.an, r.bo, r.bn = Ctx, ai, ai+1, bi, bi+1
				if ln.EOL == nil {
					r.oldMark, r.newMark = true, true
				}
				ai, bi = ai+1, bi+1
			case edit.Delete:
				r.kind, r.ao, r.an = Old, ai, ai+1
				if last && ln.EOL == nil {
					r.oldMark = true
				}
				ai++
			case edit.Insert:
				r.kind, r.bo, r.bn = New, bi, bi+1
				if last && ln.EOL == nil {
					r.newMark = true
				}
				bi++
			}
			r.line = ln
			fr = append(fr, r)
		}
	}
	for i := 0; i < len(fr); i++ {
		r := fr[i]
		if r.kind == Ctx {
			continue
		}
		c := chg{}
		if r.ao >= 0 {
			c.a0, c.a1 = r.ao, r.an
		} else {
			c.a0, c.a1 = r.bn, r.bn
		}
		if r.bo >= 0 {
			c.b0, c.b1 = r.bo, r.bn
		} else {
			c.b0, c.b1 = r.an, r.an
		}
		j := i
		for j+1 < len(fr) && fr[j+1].kind != Ctx {
			j++
		}
		if fr[j].ao >= 0 {
			c.a1 = fr[j].an
		}
		if fr[j].bo >= 0 {
			c.b1 = fr[j].bn
		}
		changes = append(changes, c)
		i = j
	}
	if len(changes) == 0 {
		return nil
	}
	var groups []chg
	g := changes[0]
	for _, c := range changes[1:] {
		gap := c.a0 - g.a1
		if gb := c.b0 - g.b1; gb < gap {
			gap = gb
		}
		if gap <= 2*C {
			g.a1, g.b1 = c.a1, c.b1
		} else {
			groups = append(groups, g)
			g = c
		}
	}
	groups = append(groups, g)

	var out []Hunk
	for _, grp := range groups {
		hiA, hiB := grp.a1+C, grp.b1+C
		loA, loB := grp.a0-C, grp.b0-C
		consA, consB := grp.a1 > max2(loA, 0), grp.b1 > max2(loB, 0)
		h := Hunk{OldStart: startNum(grp.a0, loA, consA),
			NewStart: startNum(grp.b0, loB, consB)}
		for _, r := range fr {
			inA := r.ao >= 0 && r.ao >= loA && r.ao < hiA
			inB := r.bo >= 0 && r.bo >= loB && r.bo < hiB
			if !inA && !inB {
				continue
			}
			row := Row{Kind: r.kind, Line: r.line, OldNoNL: r.oldMark, NewNoNL: r.newMark}
			h.Rows = append(h.Rows, row)
			if r.oldMark || r.newMark {
				h.Rows = append(h.Rows, Row{Kind: Mark,
					Line:    lines.Line{Content: []byte(noNLText), EOL: []byte("\n")},
					OldNoNL: r.oldMark, NewNoNL: r.newMark})
			}
			switch r.kind {
			case Ctx:
				h.OldCount++
				h.NewCount++
			case Old:
				h.OldCount++
			case New:
				h.NewCount++
			}
		}
		out = append(out, h)
	}
	return out
}

// startNum maps a 0-based span to a GNU hunk start. A side consuming lines
// uses the 1-based first line; a zero-count side names the line before the
// insertion point, i.e. its 1-based index (a0 itself), 0 at file start.
func startNum(a0, lo int, consumed bool) int {
	if consumed {
		if lo < 0 {
			return 1
		}
		return lo + 1
	}
	return a0
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
