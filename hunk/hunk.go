// Package hunk 把编辑脚本按上下文行数 C 分组为 unified diff 的 hunk。
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Hunk 是一段连续的编辑操作及它在旧/新文件中的定位。
// OldStart==0 且 OldCount==0 表示插入点在文件开头（头写 -0,0）。
type Hunk struct {
	Ops                []edit.Op
	OldStart, OldCount int
	NewStart, NewCount int
	OldNoNL, NewNoNL   bool // 该 hunk 是否触及旧/新文件“末行无换行”
}

// Diff 是便捷入口：先求最短脚本，再按 C 行上下文分组。
func Diff(a, b []lines.Line, ctx, maxD int) ([]Hunk, error) {
	ops, err := edit.Diff(a, b, maxD)
	if err != nil {
		return nil, err
	}
	return Build(ops, ctx), nil
}

type run struct{ from, to int } // 操作下标区间 [from,to)

// Build 把脚本按 DESIGN.md 第 2 节的阈值（g ≤ 2C 合并）切成 hunk。
func Build(ops []edit.Op, ctx int) []Hunk {
	runs := changeRuns(ops)
	var hs []Hunk
	for i := 0; i < len(runs); i++ {
		from, to := runs[i].from, runs[i].to
		// 与后续 run 的间隔未改动行数 ≤ 2C 则持续合并。
		for i+1 < len(runs) {
			gap := runs[i+1].from - to
			if gap > 2*ctx {
				break
			}
			to = runs[i+1].to
			i++
		}
		lo, hi := from-ctx, to+ctx
		if lo < 0 {
			lo = 0
		}
		if hi > len(ops) {
			hi = len(ops)
		}
		var o, n int
		for _, op := range ops[:lo] {
			if op.Kind != edit.Insert {
				o++
			}
			if op.Kind != edit.Delete {
				n++
			}
		}
		hs = append(hs, makeHunk(ops[lo:hi], o, n))
	}
	return hs
}

func changeRuns(ops []edit.Op) []run {
	var rs []run
	for i := 0; i < len(ops); {
		if ops[i].Kind == edit.Equal {
			i++
			continue
		}
		s := i
		for i < len(ops) && ops[i].Kind != edit.Equal {
			i++
		}
		rs = append(rs, run{s, i})
	}
	return rs
}

func makeHunk(seg []edit.Op, oldX, newY int) Hunk {
	h := Hunk{Ops: seg, OldStart: oldX, NewStart: newY}
	for _, op := range seg {
		switch op.Kind {
		case edit.Equal:
			oldX++
			newY++
			h.OldCount++
			h.NewCount++
		case edit.Delete:
			oldX++
			h.OldCount++
		case edit.Insert:
			newY++
			h.NewCount++
		}
	}
	// 计数为 0 时起始行号取“插入点前一行”（0 表示文件开头），见 DESIGN.md 第 1 节。
	if h.OldCount == 0 {
		h.OldStart = oldX
	}
	if h.NewCount == 0 {
		h.NewStart = newY
	}
	h.OldNoNL = touchesNoNL(seg, true)
	h.NewNoNL = touchesNoNL(seg, false)
	return h
}

func touchesNoNL(seg []edit.Op, old bool) bool {
	for i := len(seg) - 1; i >= 0; i-- {
		op := seg[i]
		var l lines.Line
		if old && op.Kind != edit.Insert {
			l = op.Old
		} else if !old && op.Kind != edit.Delete {
			l = op.New
		} else {
			continue
		}
		return !l.NL
	}
	return false
}
