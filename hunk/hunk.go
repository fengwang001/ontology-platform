// Package hunk 把编辑脚本按上下文行数分组成 hunk，并按 GNU diff 的
// 规则（间隔 g ≤ 2C 时合并）决定相邻 hunk 何时合并。依赖 edit。
package hunk

import "ontology/edit"

// Line 是 hunk 内的一行：Kind 为 ' '、'-'、'+'，Text 含原本的行尾。
type Line struct {
	Kind byte
	Text string
}

// Hunk 是一段修改及其上下文。AStart/ACount 是旧文件侧范围，
// BStart/BCount 是新文件侧范围；计数为 0 时 Start 是空区间前一行的行号。
type Hunk struct {
	AStart, ACount int
	BStart, BCount int
	Lines          []Line
}

// Group 把脚本切成 hunk。每段改动两侧各带 ctx 行上下文；两段改动之间
// 未改动行数 g ≤ 2*ctx 时合并为一个 hunk，否则分开。
func Group(s *edit.Script, ctx int) []Hunk {
	ops := s.Ops
	var hs []Hunk
	for i := 0; i < len(ops); {
		j := i
		for j < len(ops) && ops[j].Kind == edit.Equal {
			j++
		}
		if j == len(ops) {
			break
		}
		start := max(j-ctx, i)
		last, commons, k := j, 0, j
		for k < len(ops) {
			if ops[k].Kind != edit.Equal {
				last, commons = k, 0
			} else if commons++; commons > 2*ctx {
				break
			}
			k++
		}
		end := last + 1 + min(ctx, commons)
		hs = append(hs, build(s, ops[start:end]))
		i = end
	}
	return hs
}

// build 把一段连续操作转成 Hunk 并计算两侧范围。
func build(s *edit.Script, ops []edit.Op) Hunk {
	h := Hunk{}
	for i, op := range ops {
		switch op.Kind {
		case edit.Equal:
			h.Lines = append(h.Lines, Line{' ', s.A[op.A]})
			h.ACount++
			h.BCount++
		case edit.Del:
			h.Lines = append(h.Lines, Line{'-', s.A[op.A]})
			h.ACount++
		default:
			h.Lines = append(h.Lines, Line{'+', s.B[op.B]})
			h.BCount++
		}
		if i == 0 {
			h.AStart, h.BStart = op.A, op.B
		}
	}
	if h.ACount > 0 {
		h.AStart++
	}
	if h.BCount > 0 {
		h.BStart++
	}
	return h
}
