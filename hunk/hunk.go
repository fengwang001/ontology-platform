// Package hunk 把编辑脚本按上下文行数分组成 hunk。
// 合并规则：两段改动之间未改动行数 g ≤ 2C 时合并，否则拆分
// （推导见 DESIGN.md 第 2 节）。
package hunk

import "ontology/edit"

// Line 是 hunk 内的一行：Kind 为 ' '、'-'、'+'，Text 含原行尾。
type Line struct {
	Kind byte
	Text []byte
}

// Hunk 是一段改动及其上下文。OldStart/NewStart 为 1-based 行号；
// 对应计数为 0 时表示"前一行行号"（文件首之前为 0，见 DESIGN.md 第 1 节）。
type Hunk struct {
	OldStart, OldCount, NewStart, NewCount int
	Lines                                  []Line
}

// Group 把脚本 s 按上下文行数 ctx 分组成若干 hunk。
func Group(s edit.Script, ctx int) []Hunk {
	starts := make([][2]int, len(s)) // 每步操作的 1-based 旧/新行号
	o, n := 1, 1
	for i, op := range s {
		starts[i] = [2]int{o, n}
		if op.Kind != '+' {
			o++
		}
		if op.Kind != '-' {
			n++
		}
	}
	var clusters [][2]int // 改动簇 [首, 末] 操作下标
	for i := 0; i < len(s); {
		if s[i].Kind == ' ' {
			i++
			continue
		}
		lo, hi, j := i, i, i+1
		for j < len(s) {
			k := j
			for k < len(s) && s[k].Kind == ' ' {
				k++
			}
			if k == len(s) || k-j > 2*ctx {
				break
			}
			hi, j = k, k+1
		}
		clusters = append(clusters, [2]int{lo, hi})
		i = hi + 1
	}
	var hunks []Hunk
	for _, c := range clusters {
		lo, hi := c[0], c[1]
		for k := 0; k < ctx && lo > 0; k++ {
			lo--
		}
		for k := 0; k < ctx && hi+1 < len(s) && s[hi+1].Kind == ' '; k++ {
			hi++
		}
		h := Hunk{OldStart: starts[lo][0], NewStart: starts[lo][1]}
		for _, op := range s[lo : hi+1] {
			h.Lines = append(h.Lines, Line{op.Kind, op.Text})
			if op.Kind != '+' {
				h.OldCount++
			}
			if op.Kind != '-' {
				h.NewCount++
			}
		}
		if h.OldCount == 0 {
			h.OldStart--
		}
		if h.NewCount == 0 {
			h.NewStart--
		}
		hunks = append(hunks, h)
	}
	return hunks
}
