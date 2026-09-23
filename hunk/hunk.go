// Package hunk 把最短编辑脚本按上下文行数分组为 unified diff 的 hunk。
package hunk

import "ontology/edit"

// Kind 是 hunk 内一行的种类。
type Kind int

const (
	Context Kind = iota
	Remove
	Add
)

// Item 是 hunk 内一行；OL/NL 为该行在原文件中的原始行（含行尾）。
type Item struct {
	Kind Kind
	OL   string // Kind==Add 时为空
	NL   string // Kind==Remove 时为空
}

// Hunk 是一段连续 hunk。OStart/NStart 为 1 基起始行号（0 行区间按 GNU
// 规则取“前一行的行号”，可能为 0）；OldNoNL/NewNoNL 表示该侧末行无换行。
type Hunk struct {
	OStart, OCount int
	NStart, NCount int
	Items          []Item
	OldNoNL        bool
	NewNoNL        bool
}

// Build 把脚本按每侧 C 行上下文切分；相隔未改动行数 g ≤ 2C 的相邻改动合并。
// oldLines/newLines 为原始行（Raw 形式），用于判定末行换行与区间计数。
func Build(s *edit.Script, oldLines, newLines []string, c int) []Hunk {
	type span struct{ lo, hi int } // ops 下标区间 [lo,hi)，含上下文
	var groups []span
	i := 0
	for i < len(s.Ops) {
		if s.Ops[i].Kind == edit.Equal {
			i++
			continue
		}
		j := i
		for j < len(s.Ops) && s.Ops[j].Kind != edit.Equal {
			j++
		}
		lo, hi := i, j
		if lo-c >= 0 {
			lo -= c
		} else {
			lo = 0
		}
		if hi+c <= len(s.Ops) {
			hi += c
		} else {
			hi = len(s.Ops)
		}
		if n := len(groups); n > 0 && lo <= groups[n-1].hi {
			groups[n-1].hi = hi
		} else {
			groups = append(groups, span{lo, hi})
		}
		i = j
	}
	oldEndsNoNL := len(oldLines) > 0 && !endsNL(oldLines[len(oldLines)-1])
	newEndsNoNL := len(newLines) > 0 && !endsNL(newLines[len(newLines)-1])
	hs := make([]Hunk, 0, len(groups))
	for _, g := range groups {
		h := Hunk{}
		oc, nc := 0, 0
		lastO, lastN := -1, -1
		for _, op := range s.Ops[g.lo:g.hi] {
			switch op.Kind {
			case edit.Equal:
				h.Items = append(h.Items, Item{Kind: Context, OL: op.A.Raw(), NL: op.B.Raw()})
				oc++
				nc++
				lastO++
				lastN++
			case edit.Delete:
				h.Items = append(h.Items, Item{Kind: Remove, OL: op.A.Raw()})
				oc++
				lastO++
			case edit.Insert:
				h.Items = append(h.Items, Item{Kind: Add, NL: op.B.Raw()})
				nc++
				lastN++
			}
		}
		h.OCount, h.NCount = oc, nc
		h.OStart = startOf(s, g.lo, oc, false)
		h.NStart = startOf(s, g.lo, nc, true)
		if oldEndsNoNL && oc > 0 && lastO == len(oldLines)-1 {
			h.OldNoNL = true
		}
		if newEndsNoNL && nc > 0 && lastN == len(newLines)-1 {
			h.NewNoNL = true
		}
		hs = append(hs, h)
	}
	return hs
}

// startOf 按 GNU 规则计算起始行号：count==0 时返回“前一行”的 1 基行号。
func startOf(s *edit.Script, opLo, count int, newSide bool) int {
	before := 0
	for _, op := range s.Ops[:opLo] {
		if (newSide && op.Kind != edit.Delete) || (!newSide && op.Kind != edit.Insert) {
			before++
		}
	}
	if count == 0 {
		return before
	}
	return before + 1
}

func endsNL(line string) bool {
	return len(line) > 0 && line[len(line)-1] == '\n'
}
