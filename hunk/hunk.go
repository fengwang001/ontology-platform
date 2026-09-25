// Package hunk 把编辑脚本按上下文行数分组成 hunk，并决定相邻 hunk 的合并。
package hunk

import "ontology/edit"

// Line 是 hunk 内的一行：Kind 为 ' '、'-'、'+'，Text 含原行尾。
type Line struct {
	Kind byte
	Text []byte
}

// Hunk 是一段连续改动加上下文。AStart/ACount 描述旧文件侧，
// BStart/BCount 描述新文件侧；计数为 0 时 Start 是前一行的行号
// （即 0-based 插入下标），否则是首行的 1-based 行号（见 DESIGN.md）。
type Hunk struct {
	AStart, ACount int
	BStart, BCount int
	Lines          []Line
}

// Group 把 ops 按上下文行数 ctx 分组。两段改动之间的未改动行数
// g <= 2*ctx 时合并为一个 hunk，否则拆分（见 DESIGN.md 第 2 节）。
func Group(ops []edit.Op, ctx int) []Hunk {
	var hunks []Hunk
	n := len(ops)
	a, b := 0, 0 // ops[i] 之前的旧/新行数（0-based 下标）
	i := 0
	for i < n {
		for i < n && ops[i].Kind == ' ' {
			i++
			a++
			b++
		}
		if i == n {
			break
		}
		hs := i - ctx // hunk 起点（含前文 ctx 行上下文）
		if hs < 0 {
			hs = 0
		}
		ha, hb := a-(i-hs), b-(i-hs)
		last := i - 1 // 最后一个改动行的下标
		for j := i; j < n; {
			if ops[j].Kind != ' ' {
				last = j
				j++
				continue
			}
			k := j
			for k < n && ops[k].Kind == ' ' {
				k++
			}
			if k == n || k-j > 2*ctx {
				break
			}
			j = k
		}
		he := last + 1 + ctx // hunk 终点（含后文 ctx 行上下文）
		if he > n {
			he = n
		}
		h := Hunk{Lines: make([]Line, 0, he-hs)}
		ra, rb := ha, hb
		for _, op := range ops[hs:he] {
			h.Lines = append(h.Lines, Line{op.Kind, op.Line})
			switch op.Kind {
			case ' ':
				ra++
				rb++
			case '-':
				ra++
			case '+':
				rb++
			}
		}
		h.ACount, h.BCount = ra-ha, rb-hb
		h.AStart, h.BStart = ha+1, hb+1
		if h.ACount == 0 {
			h.AStart = ha
		}
		if h.BCount == 0 {
			h.BStart = hb
		}
		hunks = append(hunks, h)
		a, b = ra, rb
		i = he
	}
	return hunks
}
