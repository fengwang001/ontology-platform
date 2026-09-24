// Package hunk 把最短编辑脚本按上下文行数 C 分组为统一格式 hunk。
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Row 是 hunk 内一行：Kind 为 ' '/'-'/'+'; Idx 指向旧行或新行。
// OldNoNL/NewNoNL 表示该行是否是对应侧“没有行尾”的最后一行。
type Row struct {
	Kind             byte
	Idx              int
	OldNoNL, NewNoNL bool
}

// Hunk 是一个 hunk。OldStart/NewStart 为统一格式展示行号（文件头插入为 0）。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Build 用上下文 c 分组；相邻改动间隔 g≤2c 时合并。
func Build(script []edit.Op, old, new []lines.Line, c int) []Hunk {
	if c < 0 {
		c = 0
	}
	oldNoNL := len(old) > 0 && len(old[len(old)-1].End) == 0
	newNoNL := len(new) > 0 && len(new[len(new)-1].End) == 0
	lastOld, lastNew := -1, -1
	for i, op := range script {
		if op.Kind != '+' {
			lastOld = i
		}
		if op.Kind != '-' {
			lastNew = i
		}
	}
	var wins [][2]int
	for i := 0; i < len(script); {
		if script[i].Kind == '=' {
			i++
			continue
		}
		j := i
		for j < len(script) && script[j].Kind != '=' {
			j++
		}
		wins = append(wins, [2]int{i, j})
		i = j
	}
	var merged [][2]int
	for _, w := range wins {
		lo, hi := w[0]-c, w[1]+c
		if lo < 0 {
			lo = 0
		}
		if hi > len(script) {
			hi = len(script)
		}
		if n := len(merged); n > 0 && lo <= merged[n-1][1] {
			if hi > merged[n-1][1] {
				merged[n-1][1] = hi
			}
		} else {
			merged = append(merged, [2]int{lo, hi})
		}
	}
	hs := make([]Hunk, 0, len(merged))
	for _, rg := range merged {
		hs = append(hs, build(script, rg[0], rg[1], lastOld, lastNew, oldNoNL, newNoNL))
	}
	return hs
}

func build(s []edit.Op, lo, hi, lastOld, lastNew int, oldNoNL, newNoNL bool) Hunk {
	h := Hunk{}
	for t := lo; t < hi; t++ {
		op := s[t]
		row := Row{Kind: op.Kind, Idx: -1}
		if op.Kind != '+' {
			row.Idx = op.Old
			if h.OldCount == 0 {
				h.OldStart = op.Old + 1
			}
			h.OldCount++
			row.OldNoNL = oldNoNL && t == lastOld
		}
		if op.Kind != '-' {
			if op.Kind == '+' {
				row.Idx = op.New
			}
			if h.NewCount == 0 {
				h.NewStart = op.New + 1
			}
			h.NewCount++
			row.NewNoNL = newNoNL && t == lastNew
		}
		h.Rows = append(h.Rows, row)
	}
	if h.OldCount == 0 {
		h.OldStart = zeroStart(s, lo)
	}
	if h.NewCount == 0 {
		h.NewStart = zeroStart(s, lo)
	}
	return h
}

func zeroStart(s []edit.Op, lo int) int {
	if lo == 0 {
		return 0
	}
	for i := lo - 1; i >= 0; i-- {
		if s[i].Kind == '=' {
			return s[i].Old + 1
		}
	}
	return 0
}
