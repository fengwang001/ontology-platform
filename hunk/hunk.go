// Package hunk 把编辑脚本按上下文行数分组成 hunk。
package hunk

import "ontology/edit"

// Row 是 hunk 内的一行。
type Row struct {
	Kind byte // ' ' '-' '+'
	Old  int  // 旧行下标，-1 表示无
	New  int  // 新行下标，-1 表示无
}

// Hunk 是一组连续的编辑行。
type Hunk struct {
	OldStart int // 头里的 a（1 基，b=0 时为上一行行号）
	OldCount int
	NewStart int
	NewCount int
	Rows     []Row
}

// Group 按上下文 C 行分组；两个改动段间隔 > 2C 行未改动时拆开（DESIGN.md 第 2 节）。
func Group(ops []edit.Op, c int) []Hunk {
	type seg struct{ lo, hi int } // ops 下标区间 [lo,hi)
	var segs []seg
	i := 0
	for i < len(ops) {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		lo := i
		for i < len(ops) && ops[i].Kind != ' ' {
			i++
		}
		segs = append(segs, seg{lo, i})
	}
	var groups [][]seg
	for _, s := range segs {
		if len(groups) == 0 {
			groups = append(groups, []seg{s})
			continue
		}
		last := groups[len(groups)-1]
		gap := s.lo - last[len(last)-1].hi
		if gap > 2*c {
			groups = append(groups, []seg{s})
		} else {
			groups[len(groups)-1] = append(last, s)
		}
	}
	var hs []Hunk
	for _, g := range groups {
		lo := g[0].lo - c
		if lo < 0 {
			lo = 0
		}
		hi := g[len(g)-1].hi + c
		if hi > len(ops) {
			hi = len(ops)
		}
		h := Hunk{}
		for _, op := range ops[lo:hi] {
			h.Rows = append(h.Rows, Row{Kind: op.Kind, Old: op.Old, New: op.New})
		}
		fillBounds(&h, ops, lo, hi)
		hs = append(hs, h)
	}
	return hs
}

func fillBounds(h *Hunk, ops []edit.Op, lo, hi int) {
	var firstOld, firstNew int = -1, -1
	var oc, nc int
	for _, r := range h.Rows {
		if r.Kind != '+' {
			if firstOld < 0 {
				firstOld = r.Old
			}
			oc++
		}
		if r.Kind != '-' {
			if firstNew < 0 {
				firstNew = r.New
			}
			nc++
		}
	}
	switch {
	case oc == 0:
		h.OldStart, h.OldCount = oldOffset(ops, lo), 0
	default:
		h.OldStart, h.OldCount = firstOld+1, oc
	}
	switch {
	case nc == 0:
		h.NewStart, h.NewCount = newOffset(ops, lo), 0
	default:
		h.NewStart, h.NewCount = firstNew+1, nc
	}
}

func oldOffset(ops []edit.Op, lo int) int {
	n := 0
	for _, op := range ops[:lo] {
		if op.Kind != '+' {
			n++
		}
	}
	return n
}

func newOffset(ops []edit.Op, lo int) int {
	n := 0
	for _, op := range ops[:lo] {
		if op.Kind != '-' {
			n++
		}
	}
	return n
}
