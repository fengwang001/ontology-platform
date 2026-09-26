package hunk

import "ontology/edit"

type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []edit.Op
}

func Groups(ops []edit.Op, c int) []Hunk {
	var ch []int
	for _, op := range ops {
		if op.Kind != edit.Equal {
			ch = append(ch, i)
		}
	}
	if len(ch) == 0 {
		return nil
	}
	st, en := bounds(ops, ch[0], c)
	var spans [][2]int
	for _, ci := range ch[1:] {
		ns, ne := bounds(ops, ci, c)
		if ns-en-1 <= 2*c {
			en = ne
		} else {
			spans, st, en = append(spans, [2]int{st, en}), ns, ne
		}
	}
	spans = append(spans, [2]int{st, en})
	out := make([]Hunk, 0, len(spans))
	for _, s := range spans {
		out = append(out, build(ops[s[0]:s[1]+1]))
	}
	return out
}

func bounds(ops []edit.Op, i, c int) (int, int) {
	st, en := i, i
	for n := c; n > 0 && st > 0 && ops[st-1].Kind == edit.Equal; n-- {
		st--
	}
	for n := c; n > 0 && en+1 < len(ops) && ops[en+1].Kind == edit.Equal; n-- {
		en++
	}
	return st, en
}

func build(ops []edit.Op) Hunk {
	h := Hunk{Ops: ops, OldStart: -1, NewStart: -1}
	for i, op := range ops {
		if op.Kind != edit.Insert && h.OldStart < 0 {
			h.OldStart = op.OldIndex + 1
		}
		if op.Kind != edit.Delete && h.NewStart < 0 {
			h.NewStart = op.NewIndex + 1
		}
		if op.Kind != edit.Insert {
			h.OldCount++
		}
		if op.Kind != edit.Delete {
			h.NewCount++
		}
	}
	if h.OldCount == 0 {
		h.OldStart = firstChange(ops).OldIndex
	}
	if h.NewCount == 0 {
		h.NewStart = lastChange(ops).NewIndex
	}
	return h
}

func firstChange(ops []edit.Op) edit.Op {
	for _, op := range ops {
		if op.Kind != edit.Equal {
			return op
		}
	}
	return edit.Op{}
}

func lastChange(ops []edit.Op) edit.Op {
	for i := len(ops) - 1; i >= 0; i-- {
		if ops[i].Kind != edit.Equal {
			return ops[i]
		}
	}
	return edit.Op{}
}
