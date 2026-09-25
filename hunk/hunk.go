// Package hunk 把编辑脚本按上下文行数 C 分组为统一格式的 hunk。
package hunk

import (
	"ontology/edit"
)

// Hunk 是一段连续的统一 diff 片段。行号遵循 GNU 约定（见 DESIGN.md 1）。
type Hunk struct {
	OldStart int // 1 起；OldCount==0 时为前一行行号（可为 0）
	OldCount int
	NewStart int
	NewCount int
	Ops      []edit.Op
}

type pos struct {
	o, n int // 该 op 之前已消耗的旧/新行号（0 起）
}

// Build 把脚本按上下文 C 分 hunk。合并规则：相邻改动间隔 g<=2C 合并
// （DESIGN.md 2）。无改动（全 Equal）时返回空切片。
func Build(ops []edit.Op, C int) []Hunk {
	if C < 0 {
		C = 0
	}
	ps := make([]pos, len(ops))
	oi, ni := 0, 0
	for i, op := range ops {
		ps[i] = pos{oi, ni}
		switch op.Kind {
		case edit.Equal:
			oi++
			ni++
		case edit.Delete:
			oi++
		case edit.Insert:
			ni++
		}
	}
	var groups [][2]int // [起, 止] op 下标，已按 2C 规则合并
	var first, last int = -1, -1
	flush := func() {
		if first >= 0 {
			groups = append(groups, [2]int{first, last})
		}
		first, last = -1, -1
	}
	for i, op := range ops {
		if op.Kind == edit.Equal {
			continue
		}
		if first < 0 {
			first, last = i, i
			continue
		}
		gap := i - last - 1
		if gap <= 2*C {
			last = i
		} else {
			flush()
			first, last = i, i
		}
	}
	flush()
	hunks := make([]Hunk, 0, len(groups))
	for _, g := range groups {
		s, e := g[0], g[1]
		for k := 0; k < C && s > 0 && ops[s-1].Kind == edit.Equal; k++ {
			s--
		}
		for k := 0; k < C && e < len(ops)-1 && ops[e+1].Kind == edit.Equal; k++ {
			e++
		}
		h := Hunk{Ops: append([]edit.Op(nil), ops[s:e+1]...)}
		for _, op := range h.Ops {
			if op.Kind == edit.Equal || op.Kind == edit.Delete {
				h.OldCount++
			}
			if op.Kind == edit.Equal || op.Kind == edit.Insert {
				h.NewCount++
			}
		}
		beforeOld, beforeNew := ps[s].o, ps[s].n
		if h.OldCount == 0 {
			h.OldStart = beforeOld
		} else {
			h.OldStart = beforeOld + 1
		}
		if h.NewCount == 0 {
			h.NewStart = beforeNew
		} else {
			h.NewStart = beforeNew + 1
		}
		hunks = append(hunks, h)
	}
	return hunks
}
