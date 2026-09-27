// Package hunk 把编辑脚本按上下文行数 C 分组成 hunk 并决定合并（g≤2C 合并）。
package hunk

import "ontology/edit"

// Hunk 是一段统一格式 hunk（行号从 1 起，0 表示空文件侧的锚点）。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []edit.Op
}

// Build 把脚本切为 hunk。相邻改动间未改动行数 g≤2C 时合并为一个 hunk。
func Build(ops []edit.Op, c int) []Hunk {
	// 标记每段改动的 [变更开始, 变更结束)（op 下标）。
	type span struct{ s, e int }
	var spans []span
	for i := 0; i < len(ops); {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		s := i
		for i < len(ops) && ops[i].Kind != ' ' {
			i++
		}
		spans = append(spans, span{s, i})
	}
	if len(spans) == 0 {
		return nil
	}
	// 合并：两段间相等行 = gap 个 op；gap≤2C 则合并。
	type block struct{ s, e int }
	blocks := []block{{spans[0].s, spans[0].e}}
	for _, sp := range spans[1:] {
		last := &blocks[len(blocks)-1]
		gap := sp.s - last.e
		if gap <= 2*c {
			last.e = sp.e
		} else {
			blocks = append(blocks, block{sp.s, sp.e})
		}
	}
	var out []Hunk
	for _, blk := range blocks {
		s := blk.s - c
		if s < 0 {
			s = 0
		}
		e := blk.e + c
		if e > len(ops) {
			e = len(ops)
		}
		h := Hunk{Ops: append([]edit.Op(nil), ops[s:e]...)}
		leadOld, leadNew := 0, 0
		for i := 0; i < s; i++ {
			switch ops[i].Kind {
			case ' ':
				leadOld++
				leadNew++
			case '-':
				leadOld++
			case '+':
				leadNew++
			}
		}
		for _, o := range h.Ops {
			switch o.Kind {
			case ' ':
				h.OldCount++
				h.NewCount++
			case '-':
				h.OldCount++
			case '+':
				h.NewCount++
			}
		}
		// 计数为 0：行号取前一锚点行号（lead），否则从 lead+1 起。
		if h.OldCount == 0 {
			h.OldStart = leadOld
		} else {
			h.OldStart = leadOld + 1
		}
		if h.NewCount == 0 {
			h.NewStart = leadNew
		} else {
			h.NewStart = leadNew + 1
		}
		out = append(out, h)
	}
	return out
}
