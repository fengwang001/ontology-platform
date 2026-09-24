// Package hunk groups a shortest edit script into unified-diff hunks with a
// configurable context width. Two change groups separated by g unchanged
// lines are merged when g <= 2*C.
package hunk

import "ontology/edit"

// Row is one rendered hunk row. NoNL marks the "\ No newline at end of file"
// marker that must immediately follow this row in the output.
type Row struct {
	Op    byte
	Text  []byte // content without terminator
	CRLF  bool   // terminator was "\r\n"
	HasNL bool   // row line had a terminator
	NoNL  bool   // marker follows this row
}

// Hunk is one @@ block. OldStart/NewStart are 1-based; zero count yields the
// "previous line number" per unified-diff convention (0 at file start).
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Build converts a script into hunks with C lines of context.
func Build(script []edit.Step, c int) []Hunk {
	ch := changeGroups(script)
	var spans [][2]int
	for _, g := range ch {
		s := g[0] - c
		if s < 0 {
			s = 0
		}
		e := g[1] + c
		if e > len(script) {
			e = len(script)
		}
		if n := len(spans); n > 0 && s <= spans[n-1][1] {
			spans[n-1][1] = e
		} else {
			spans = append(spans, [2]int{s, e})
		}
	}
	var out []Hunk
	for _, sp := range spans {
		out = append(out, makeHunk(script, sp[0], sp[1]))
	}
	return out
}

func changeGroups(s []edit.Step) [][2]int {
	var g [][2]int
	for i := 0; i < len(s); i++ {
		if s[i].Op == edit.Equal {
			continue
		}
		j := i
		for j < len(s) && s[j].Op != edit.Equal {
			j++
		}
		g = append(g, [2]int{i, j})
		i = j
	}
	return g
}

func makeHunk(s []edit.Step, lo, hi int) Hunk {
	var nOld, nNew int
	var preOld, preNew int
	for i, st := range s {
		old, neu := st.Op != edit.Ins, st.Op != edit.Del
		if i < lo {
			if old {
				preOld++
			}
			if neu {
				preNew++
			}
		}
		if old {
			nOld++
		}
		if neu {
			nNew++
		}
	}
	oldPos, newPos := preOld, preNew
	h := Hunk{OldStart: preOld, NewStart: preNew}
	for i := lo; i < hi; i++ {
		st := s[i]
		r := Row{Op: st.Op}
		r.Text = st.Line.Content()
		r.HasNL = st.Line.HasNL()
		if r.HasNL && len(st.Line) >= 2 && st.Line[len(st.Line)-2] == '\r' {
			r.CRLF = true
		}
		oldEnd := (st.Op != edit.Ins) && !r.HasNL && oldPos+1 == nOld
		newEnd := (st.Op != edit.Del) && !r.HasNL && newPos+1 == nNew
		if oldEnd || newEnd {
			r.NoNL = true
		}
		switch st.Op {
		case edit.Equal:
			oldPos, newPos = oldPos+1, newPos+1
			h.OldCount, h.NewCount = h.OldCount+1, h.NewCount+1
		case edit.Del:
			oldPos++
			h.OldCount++
		case edit.Ins:
			newPos++
			h.NewCount++
		}
		h.Rows = append(h.Rows, r)
	}
	if h.OldCount > 0 {
		h.OldStart = preOld + 1
	}
	if h.NewCount > 0 {
		h.NewStart = preNew + 1
	}
	return h
}
