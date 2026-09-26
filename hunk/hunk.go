// Package hunk 把编辑脚本按上下文行数 C 分组为 unified-diff hunk。
package hunk

import "ontology/edit"

// Hunk 是一个连续的 hunk。Start/Count 为 unified 头里的数值（0 基插入点语义）。
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Ops      []edit.Op // '=' 为上下文，'-'/'+' 为改动
}

// Build 把脚本切成若干 hunk。相邻改动间隔 ≤ 2C 行未改动时合并。
// 返回 hunk 以及旧、新文件的总行数。
func Build(script []edit.Op, c int) ([]Hunk, int, int) {
	totalOld, totalNew := 0, 0
	for _, o := range script {
		if o.Kind == '=' || o.Kind == '-' {
			totalOld++
		}
		if o.Kind == '=' || o.Kind == '+' {
			totalNew++
		}
	}
	type span struct{ lo, hi int } // 脚本下标区间，[lo,hi) 内首个/末个为改动
	groups := []span{}
	i := 0
	for i < len(script) {
		if script[i].Kind == '=' {
			i++
			continue
		}
		lo := i
		for i < len(script) && script[i].Kind != '=' {
			i++
		}
		hi := i
		for i < len(script) && script[i].Kind == '=' {
			j := i
			for j < len(script) && script[j].Kind == '=' {
				j++
			}
			if j == len(script) || j-i > 2*c {
				break
			}
			i = j
			for i < len(script) && script[i].Kind != '=' {
				i++
			}
			hi = i
		}
		groups = append(groups, span{lo, hi})
	}
	hunks := make([]Hunk, 0, len(groups))
	for _, g := range groups {
		lo, hi := g.lo, g.hi
		pad := 0
		for lo > 0 && script[lo-1].Kind == '=' && pad < c {
			lo--
			pad++
		}
		pad = 0
		for hi < len(script) && script[hi].Kind == '=' && pad < c {
			hi++
			pad++
		}
		oldBefore, newBefore := 0, 0
		for _, o := range script[:lo] {
			if o.Kind == '=' || o.Kind == '-' {
				oldBefore++
			}
			if o.Kind == '=' || o.Kind == '+' {
				newBefore++
			}
		}
		h := Hunk{OldStart: oldBefore + 1, NewStart: newBefore + 1, Ops: append([]edit.Op(nil), script[lo:hi]...)}
		for _, o := range h.Ops {
			if o.Kind == '=' || o.Kind == '-' {
				h.OldCount++
			}
			if o.Kind == '=' || o.Kind == '+' {
				h.NewCount++
			}
		}
		if h.OldCount == 0 {
			h.OldStart = oldBefore
		}
		if h.NewCount == 0 {
			h.NewStart = newBefore
		}
		hunks = append(hunks, h)
	}
	return hunks, totalOld, totalNew
}
