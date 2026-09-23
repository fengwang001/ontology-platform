// Package hunk 把编辑脚本按上下文行数分组成 hunk。
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Line 是 hunk 里的一行：Kind 为 ' '、'-' 或 '+'；
// Text 为去掉行尾的内容，NoEOL 表示原行没有 "\n" 行尾。
type Line struct {
	Kind  byte
	Text  string
	NoEOL bool
}

// Hunk 是一段改动及其上下文。Start 为 1-based 首行行号；
// Count 为 0 时 Start 表示「之前保留的行数」（见 DESIGN.md 第 1 节）。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Build 把脚本按上下文行数 ctx 分组：相邻改动之间未改动行数
// g <= 2*ctx 时合并为一个 hunk，否则分开（见 DESIGN.md 第 2 节）。
func Build(ops []edit.Op, ctx int) []Hunk {
	var hunks []Hunk
	n := len(ops)
	for i := 0; i < n; {
		if ops[i].Kind == edit.Keep {
			i++
			continue
		}
		end := i
		for j := i; j < n; {
			if ops[j].Kind != edit.Keep {
				end = j
				j++
				continue
			}
			k := j
			for k < n && ops[k].Kind == edit.Keep {
				k++
			}
			if k == n || k-j > 2*ctx {
				break
			}
			j = k
		}
		lo := i - ctx
		if lo < 0 {
			lo = 0
		}
		hi := end + 1 + ctx
		if hi > n {
			hi = n
		}
		hunks = append(hunks, build(ops, lo, hi))
		i = hi
	}
	return hunks
}

// build 由 ops[lo:hi] 构造一个 hunk 并计算两侧起始行号与计数。
func build(ops []edit.Op, lo, hi int) Hunk {
	var h Hunk
	for _, o := range ops[:lo] {
		if o.Kind != edit.Ins {
			h.OldStart++
		}
		if o.Kind != edit.Del {
			h.NewStart++
		}
	}
	baseOld, baseNew := h.OldStart, h.NewStart
	for _, o := range ops[lo:hi] {
		body, eol := lines.Text(lines.Line(o.Text))
		switch o.Kind {
		case edit.Keep:
			h.OldCount++
			h.NewCount++
			h.Lines = append(h.Lines, Line{' ', body, !eol})
		case edit.Del:
			h.OldCount++
			h.Lines = append(h.Lines, Line{'-', body, !eol})
		case edit.Ins:
			h.NewCount++
			h.Lines = append(h.Lines, Line{'+', body, !eol})
		}
	}
	if h.OldCount > 0 {
		h.OldStart = baseOld + 1
	}
	if h.NewCount > 0 {
		h.NewStart = baseNew + 1
	}
	return h
}
