// Package hunk 把编辑脚本按上下文行数分组成 hunk。
// 相邻变更间隔 g ≤ 2*context 时合并（见 DESIGN.md 第 2 节）。
package hunk

import "ontology/edit"

// Line 是 hunk 内的一行，Kind 为 ' '、'-' 或 '+'，Text 含原始行尾。
type Line struct {
	Kind byte
	Text []byte
}

// Hunk 是一段变更及其上下文。行号为 1 基；计数为 0 时行号为前一行行号。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

type sym struct {
	kind byte
	text []byte
}

// Build 把 ops 按 context 行上下文分组为 hunk 列表。
func Build(a, b [][]byte, ops []edit.Op, context int) []Hunk {
	var syms []sym
	ai, bi := 0, 0
	for _, o := range ops {
		switch o.Kind {
		case edit.Keep:
			for t := 0; t < o.A; t++ {
				syms = append(syms, sym{' ', a[ai]})
				ai++
				bi++
			}
		case edit.Del:
			for t := 0; t < o.A; t++ {
				syms = append(syms, sym{'-', a[ai]})
				ai++
			}
		case edit.Ins:
			for t := 0; t < o.B; t++ {
				syms = append(syms, sym{'+', b[bi]})
				bi++
			}
		}
	}
	var hunks []Hunk
	r, oldR, newR := 0, 0, 0
	for i := 0; i < len(syms); {
		if syms[i].kind == ' ' {
			i++
			continue
		}
		j, end := i, i
		for j < len(syms) {
			for j < len(syms) && syms[j].kind != ' ' {
				j++
			}
			end = j
			g := 0
			for j+g < len(syms) && syms[j+g].kind == ' ' {
				g++
			}
			if g > 2*context {
				break
			}
			j += g
		}
		lo, hi := i-context, end+context
		if lo < 0 {
			lo = 0
		}
		if hi > len(syms) {
			hi = len(syms)
		}
		for r < lo {
			if syms[r].kind != '+' {
				oldR++
			}
			if syms[r].kind != '-' {
				newR++
			}
			r++
		}
		h := Hunk{OldStart: oldR + 1, NewStart: newR + 1}
		for _, s := range syms[lo:hi] {
			h.Lines = append(h.Lines, Line{s.kind, s.text})
			if s.kind != '+' {
				h.OldCount++
			}
			if s.kind != '-' {
				h.NewCount++
			}
		}
		if h.OldCount == 0 {
			h.OldStart = oldR
		}
		if h.NewCount == 0 {
			h.NewStart = newR
		}
		hunks = append(hunks, h)
		i = j
	}
	return hunks
}
