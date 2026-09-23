// Package hunk 把最短编辑脚本按上下文行数 C 分组为统一格式 hunk。
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Item 是 hunk 内的一行：Kind 为 ' '、'-'、'+'；Ai、Bi 为旧/新侧行索引。
type Item struct {
	Kind byte
	Ai   int
	Bi   int
}

// Hunk 是一个统一格式 hunk。A0/B0 为 0-based 起点（b 或 d 为 0 时表示锚点）。
type Hunk struct {
	A0, ACount int
	B0, BCount int
	Items      []Item
}

// Build 按上下文行数 C（C<0 视为 0）分组。无改动时返回 nil。
func Build(sc edit.Script, a, b []lines.Line, c int) []Hunk {
	if c < 0 {
		c = 0
	}
	items := flatten(sc)
	var changeIdx []int
	for i, it := range items {
		if it.Kind != ' ' {
			changeIdx = append(changeIdx, i)
		}
	}
	if len(changeIdx) == 0 {
		return nil
	}
	groups := [][2]int{{changeIdx[0], changeIdx[0]}}
	for _, idx := range changeIdx[1:] {
		last := &groups[len(groups)-1]
		gap := 0
		for j := last[1] + 1; j < idx; j++ {
			gap++
		}
		if gap <= 2*c {
			last[1] = idx
		} else {
			groups = append(groups, [2]int{idx, idx})
		}
	}
	var out []Hunk
	for ci, g := range groups {
		lo, hi := g[0], g[1]
		x0, x1 := lo-c, hi+c
		if x0 < 0 {
			x0 = 0
		}
		if x1 > len(items)-1 {
			x1 = len(items) - 1
		}
		if ci > 0 {
			if p := groups[ci-1][1] + c; x0 < p {
				x0 = p
			}
		}
		if ci+1 < len(groups) {
			if n := groups[ci+1][0] - c; x1 > n {
				x1 = n
			}
		}
		out = append(out, makeHunk(items[x0:x1+1]))
	}
	return out
}

func flatten(sc edit.Script) []Item {
	var its []Item
	for _, op := range sc {
		ai, bi := op.A0, op.B0
		switch op.Kind {
		case ' ':
			for ai < op.A1 {
				its = append(its, Item{' ', ai, bi})
				ai++
				bi++
			}
		case '-':
			for ai < op.A1 {
				its = append(its, Item{'-', ai, -1})
				ai++
			}
		case '+':
			for bi < op.B1 {
				its = append(its, Item{'+', -1, bi})
				bi++
			}
		}
	}
	return its
}

func makeHunk(its []Item) Hunk {
	h := Hunk{Items: its}
	firstA, hasA := -1, false
	firstB, hasB := -1, false
	for _, it := range its {
		if it.Kind != '+' {
			if !hasA {
				firstA, hasA = it.Ai, true
			}
			h.ACount++
		}
		if it.Kind != '-' {
			if !hasB {
				firstB, hasB = it.Bi, true
			}
			h.BCount++
		}
	}
	if hasA {
		h.A0 = firstA + 1
	} else {
		h.A0 = anchor(its, true)
	}
	if hasB {
		h.B0 = firstB + 1
	} else {
		h.B0 = anchor(its, false)
	}
	return h
}

// anchor 计算零计数侧的 1-based 锚点：插入点前一行行号，文件首为 0。
func anchor(its []Item, aSide bool) int {
	for i := len(its) - 1; i >= 0; i-- {
		if aSide && its[i].Kind != '+' {
			return its[i].Ai + 1
		}
		if !aSide && its[i].Kind != '-' {
			return its[i].Bi + 1
		}
	}
	return 0
}
