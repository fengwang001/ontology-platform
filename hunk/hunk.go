// Package hunk 把编辑脚本按上下文行数分组为 unified-diff hunk。
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Kind 是 hunk 正文行类型。
type Kind int

const (
	Context Kind = iota
	Removed
	Added
)

// Entry 是一条 hunk 正文行，保留原始行尾信息。
type Entry struct {
	Kind Kind
	Line lines.Line
}

// Hunk 是一个分组。
type Hunk struct {
	OldStart, OldCount int // 1 起；OldCount 可为 0
	NewStart, NewCount int
	Body               []Entry
}

// Build 按上下文 C（合并阈值 2C）分组。
func Build(r edit.Result, C int) []Hunk {
	ops := r.Script
	if C < 0 {
		C = 0
	}
	var changeIdx []int
	for i, o := range ops {
		if o.Kind != edit.Equal {
			changeIdx = append(changeIdx, i)
		}
	}
	if len(changeIdx) == 0 {
		return nil
	}
	type span struct{ lo, hi int } // ops 下标，闭区间
	var spans []span
	lo, hi := changeIdx[0], changeIdx[0]
	for _, idx := range changeIdx[1:] {
		g := 0
		for t := hi + 1; t < idx; t++ {
			if ops[t].Kind == edit.Equal {
				g++
			}
		}
		if g <= 2*C {
			hi = idx
			continue
		}
		spans = append(spans, span{lo, hi})
		lo, hi = idx, idx
	}
	spans = append(spans, span{lo, hi})
	var out []Hunk
	for _, sp := range spans {
		s, e := sp.lo, sp.hi
		for c := 0; c < C && s > 0 && ops[s-1].Kind == edit.Equal; c++ {
			s--
		}
		for c := 0; c < C && e < len(ops)-1 && ops[e+1].Kind == edit.Equal; c++ {
			e++
		}
		out = append(out, makeHunk(ops, s, e))
	}
	return out
}

func makeHunk(ops []edit.Op, s, e int) Hunk {
	h := Hunk{}
	beforeOld, beforeNew := 0, 0
	for i := 0; i < s; i++ {
		switch ops[i].Kind {
		case edit.Equal:
			beforeOld++
			beforeNew++
		case edit.Delete:
			beforeOld++
		case edit.Insert:
			beforeNew++
		}
	}
	for i := s; i <= e; i++ {
		o := ops[i]
		switch o.Kind {
		case edit.Equal:
			h.Body = append(h.Body, Entry{Context, o.Line})
			h.OldCount++
			h.NewCount++
		case edit.Delete:
			h.Body = append(h.Body, Entry{Removed, o.Line})
			h.OldCount++
		case edit.Insert:
			h.Body = append(h.Body, Entry{Added, o.Line})
			h.NewCount++
		}
	}
	h.OldStart = beforeOld
	h.NewStart = beforeNew
	if h.OldCount > 0 {
		h.OldStart++
	}
	if h.NewCount > 0 {
		h.NewStart++
	}
	return h
}
