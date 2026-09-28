// Package hunk 把最短编辑脚本按上下文行数 C 分组成 unified diff 的 hunk。
package hunk

import "ontology/edit"

// Row 是 hunk 内一行。NoNL 表示该行（旧侧或新侧的最后一条对应行）无行尾，
// 渲染时其后跟 "\ No newline at end of file"。
type Row struct {
	Kind                 uint8 // ' ' 上下文, '-' 删除, '+' 插入
	Text                 string
	OldEnd, NewEnd       string // 旧/新侧原始行尾："\n"、"\r\n" 或 ""
}

// Hunk 是一个 hunk。OldStart/NewStart 为 1 起行号，零计数时空区间为前行号（见 DESIGN.md）。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Group 按上下文 C 分组，相隔 <= 2C 的改动合并（见 DESIGN.md 第 2 节）。
func Group(ops []edit.Op, c int) []Hunk {
	type span struct{ lo, hi int } // 改动在 ops 中的下标区间 [lo,hi)
	var spans []span
	for i := 0; i < len(ops); {
		if ops[i].Kind == 'e' {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].Kind != 'e' {
			j++
		}
		spans = append(spans, span{i, j})
		i = j
	}
	var hs []Hunk
	for s := 0; s < len(spans); s++ {
		lo := spans[s].lo - c
		if lo < 0 {
			lo = 0
		}
		hi := spans[s].hi + c
		for s+1 < len(spans) {
			gap := spans[s+1].lo - spans[s].hi
			if gap > 2*c {
				break
			}
			hi = spans[s+1].hi + c
			s++
		}
		if hi > len(ops) {
			hi = len(ops)
		}
		hs = append(hs, build(ops, lo, hi, len(ops)))
	}
	return hs
}

func build(ops []edit.Op, lo, hi, total int) Hunk {
	h := Hunk{OldStart: 1, NewStart: 1}
	for _, op := range ops[lo:hi] {
		if op.Kind != 'i' {
			h.OldCount++
		}
		if op.Kind != 'd' {
			h.NewCount++
		}
	}
	for k := lo; k < hi; k++ {
		op := ops[k]
		row := Row{Kind: op.Kind}
		switch op.Kind {
		case 'e':
			row.Text = op.A.Text
			row.OldEnd, row.NewEnd = op.A.End, op.B.End
		case 'd':
			row.Text = op.A.Text
			row.OldEnd = op.A.End
		case 'i':
			row.Text = op.B.Text
			row.NewEnd = op.B.End
		}
		h.Rows = append(h.Rows, row)
	}
	fixStarts(&h, ops, lo, hi)
	return h
}

func fixStarts(h *Hunk, ops []edit.Op, lo, hi int) {
	// 零计数空区间行号取前行号：开头则 0。
	h.OldStart = countPos(ops[:lo], false)
	h.NewStart = countPos(ops[:lo], true)
	if h.OldCount > 0 {
		h.OldStart++
	}
	if h.NewCount > 0 {
		h.NewStart++
	}
}

func countPos(ops []edit.Op, isNew bool) int {
	p := 0
	for _, op := range ops {
		if isNew {
			if op.Kind != 'd' {
				p++
			}
		} else if op.Kind != 'i' {
			p++
		}
	}
	return p
}
