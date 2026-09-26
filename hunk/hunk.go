// Package hunk 把编辑脚本按上下文行数 C 分组为若干 unified-diff hunk，
// 相邻改动间隔 g<=2C 时合并（见 DESIGN.md 2）。
package hunk

import "ontology/edit"

// Hunk 是一段连续的编辑操作及其在旧/新文件中的 1 基起始行。
type Hunk struct {
	OldStart int
	NewStart int
	Ops      []edit.Op
}

// Split 把脚本按每个改动块周围 C 行上下文分组。无改动（脚本全是 Equal）
// 时返回 nil。
func Split(ops []edit.Op, c int) []Hunk {
	// oldAt/newAt[i] 为 ops[i] 之前已经消费的旧/新行数。
	oldAt := make([]int, len(ops)+1)
	newAt := make([]int, len(ops)+1)
	for i, op := range ops {
		oldAt[i+1], newAt[i+1] = oldAt[i], newAt[i]
		switch op.Kind {
		case edit.Equal:
			oldAt[i+1]++
			newAt[i+1]++
		case edit.Delete:
			oldAt[i+1]++
		case edit.Insert:
			newAt[i+1]++
		}
	}

	// 收集改动块（连续非 Equal 操作）的 [lo,hi)。
	type seg struct{ lo, hi int }
	var segs []seg
	for i := 0; i < len(ops); {
		if ops[i].Kind == edit.Equal {
			i++
			continue
		}
		lo := i
		for i < len(ops) && ops[i].Kind != edit.Equal {
			i++
		}
		segs = append(segs, seg{lo, i})
	}
	if len(segs) == 0 {
		return nil
	}

	// 贪心合并：相邻块之间的 Equal（未改动）行数 gap<=2C 则并为一个 hunk。
	type span struct{ lo, hi int }
	var spans []span
	cur := span{segs[0].lo, segs[0].hi}
	for _, s := range segs[1:] {
		if s.lo-cur.hi <= 2*c {
			cur.hi = s.hi
		} else {
			spans = append(spans, cur)
			cur = span{s.lo, s.hi}
		}
	}
	spans = append(spans, cur)

	hs := make([]Hunk, 0, len(spans))
	for _, sp := range spans {
		lo, hi := sp.lo-c, sp.hi+c
		if lo < 0 {
			lo = 0
		}
		if hi > len(ops) {
			hi = len(ops)
		}
		oldStart, newStart := oldAt[lo], newAt[lo]
		switch ops[lo].Kind {
		case edit.Equal, edit.Delete:
			oldStart++ // 首操作消费旧行：从该行起
		}
		switch ops[lo].Kind {
		case edit.Equal, edit.Insert:
			newStart++ // 首操作消费新行：从该行起
		}
		hs = append(hs, Hunk{
			OldStart: oldStart,
			NewStart: newStart,
			Ops:      append([]edit.Op(nil), ops[lo:hi]...),
		})
	}
	return hs
}

// Counts 返回一个 hunk 在旧/新文件中覆盖的行数。
func (h Hunk) Counts() (oldN, newN int) {
	for _, op := range h.Ops {
		switch op.Kind {
		case edit.Equal:
			oldN++
			newN++
		case edit.Delete:
			oldN++
		case edit.Insert:
			newN++
		}
	}
	return oldN, newN
}
