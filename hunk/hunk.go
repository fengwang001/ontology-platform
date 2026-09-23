// Package hunk 把编辑脚本按上下文行数分组为 unified-diff hunk。
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Row 是 hunk 内一行：Kind 为 ' '/' -'/'+'，Line 为对应原文行（含行尾）。
type Row struct {
	Kind byte
	Line lines.Line
}

// Hunk 是一个 hunk；头计数采用 GNU 风格（长度 0 时起点为前一行行号）。
type Hunk struct {
	OldStart, OldLen int
	NewStart, NewLen int
	Rows             []Row
}

type seg struct {
	start, end int // ops 的 [start,end)
	o0, n0     int // 该段前的旧/新行下标
	oe, ne     int // 该段后的旧/新行下标（不含）
}

// Build 把最短编辑脚本按上下文 C 行分组（相隔相等行 g<=2C 时合并）。
func Build(a, b []lines.Line, ops []edit.Op, c int) []Hunk {
	if len(ops) == 0 {
		return nil
	}
	var segs []seg
	ai, bi := 0, 0
	i := 0
	for i < len(ops) {
		// 先越过相等行，直到到达下一个变化点。
		for i < len(ops) && (ops[i].Ai != ai || ops[i].Bi != bi) {
			ai, bi = ai+1, bi+1
		}
		s := seg{start: i, o0: ai, n0: bi}
		for i < len(ops) && ops[i].Ai == ai && ops[i].Bi == bi {
			if ops[i].Kind == edit.Del {
				ai++
			} else {
				bi++
			}
			i++
		}
		s.end, s.oe, s.ne = i, ai, bi
		segs = append(segs, s)
	}
	// 合并：相邻段之间相等行数 = 下一段 o0-上一段 oe（两侧相同），g<=2C 合并。
	groups := [][]seg{{segs[0]}}
	for k := 1; k < len(segs); k++ {
		g := segs[k].o0 - groups[len(groups)-1][len(groups[len(groups)-1])-1].oe
		if g <= 2*c {
			groups[len(groups)-1] = append(groups[len(groups)-1], segs[k])
		} else {
			groups = append(groups, []seg{segs[k]})
		}
	}
	out := make([]Hunk, 0, len(groups))
	for _, grp := range groups {
		out = append(out, buildOne(a, b, ops, grp, c))
	}
	return out
}

func buildOne(a, b []lines.Line, ops []edit.Op, grp []seg, c int) Hunk {
	first, last := grp[0], grp[len(grp)-1]
	pre := min(c, first.o0, first.n0)
	o0, n0 := first.o0-pre, first.n0-pre
	postO := len(a) - last.oe
	postN := len(b) - last.ne
	post := min(c, postO, postN)
	oe, ne := last.oe+post, last.ne+post
	delAt := map[int]lines.Line{}
	insAt := map[int]lines.Line{}
	for _, s := range grp {
		for _, op := range ops[s.start:s.end] {
			if op.Kind == edit.Del {
				delAt[op.Ai] = op.Line
			} else {
				insAt[op.Bi] = op.Line
			}
		}
	}
	h := Hunk{}
	emit := func(k byte, ln lines.Line) {
		h.Rows = append(h.Rows, Row{k, ln})
		if k != '+' {
			h.OldLen++
		}
		if k != '-' {
			h.NewLen++
		}
	}
	ai, bi := o0, n0
	for ai < oe || bi < ne {
		if ln, ok := delAt[ai]; ok {
			emit('-', ln)
			ai++
			continue
		}
		if ln, ok := insAt[bi]; ok {
			emit('+', ln)
			bi++
			continue
		}
		emit(' ', a[ai])
		ai, bi = ai+1, bi+1
	}
	h.OldStart = headNum(o0, h.OldLen)
	h.NewStart = headNum(n0, h.NewLen)
	return h
}

func headNum(start0, length int) int {
	if length == 0 {
		return start0
	}
	return start0 + 1
}
