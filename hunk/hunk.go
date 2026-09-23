// Package hunk 把编辑脚本按上下文行数分组成 hunk；
// 两段改动间未改动行数 g <= 2C 时合并，否则分裂（与 GNU diff -U C 一致）。
package hunk

import "ontology/edit"

// Line 是 hunk 内的一行，Kind 为 ' '、'-' 或 '+'，Text 含原始行尾。
type Line struct {
	Kind byte
	Text string
}

// Hunk 描述一段改动。OldStart/NewStart 为 0 基起始下标（区间前的行数），
// OldCount/NewCount 为两侧行数；计数为 0 时 Start 即"前一行的行号"。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Group 把 ops 按上下文行数 c 分组成若干 hunk，行文本取自 a（旧）与 b（新）。
func Group(ops []edit.Op, a, b []string, c int) []Hunk {
	var hs []Hunk
	i := 0
	for i < len(ops) {
		for i < len(ops) && ops[i].Kind == ' ' {
			i++
		}
		if i == len(ops) {
			break
		}
		lo := i - c
		if lo < 0 {
			lo = 0
		}
		hi := i
		for hi < len(ops) {
			k := hi
			for k < len(ops) && ops[k].Kind == ' ' {
				k++
			}
			if g := k - hi; k == len(ops) {
				hi += c
				if hi > len(ops) {
					hi = len(ops)
				}
				break
			} else if g > 2*c {
				hi += c
				break
			}
			hi = k
			for hi < len(ops) && ops[hi].Kind != ' ' {
				hi++
			}
		}
		hs = append(hs, build(ops[lo:hi], a, b))
		i = hi
	}
	return hs
}

func build(ops []edit.Op, a, b []string) Hunk {
	h := Hunk{OldStart: ops[0].A, NewStart: ops[0].B}
	for _, o := range ops {
		text := ""
		if o.Kind != '+' {
			h.OldCount++
			text = a[o.A]
		}
		if o.Kind != '-' {
			h.NewCount++
			text = b[o.B]
		}
		h.Lines = append(h.Lines, Line{Kind: o.Kind, Text: text})
	}
	return h
}
