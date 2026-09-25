package hunk

import "ontology/edit"

// Hunk 是一组带上下文的编辑步。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []edit.Op
}

// Group 按上下文 C 行分组，间隔 g<=2C 的改动合并。
func Group(ops []edit.Op, c int) []Hunk {
	var ch []int
	for i, o := range ops {
		if o.Kind != edit.Equal {
			ch = append(ch, i)
		}
	}
	if len(ch) == 0 {
		return nil
	}
	type span struct{ lo, hi int }
	groups := []span{{ch[0], ch[0]}}
	for _, idx := range ch[1:] {
		g := &groups[len(groups)-1]
		if idx-g.hi-1 <= 2*c {
			g.hi = idx
		} else {
			groups = append(groups, span{idx, idx})
		}
	}
	out := make([]Hunk, 0, len(groups))
	for _, gp := range groups {
		lo, hi := gp.lo, gp.hi
		for k := 0; k < c && lo-1 >= 0 && ops[lo-1].Kind == edit.Equal; k++ {
			lo--
		}
		for k := 0; k < c && hi+1 < len(ops) && ops[hi+1].Kind == edit.Equal; k++ {
			hi++
		}
		hop := ops[lo : hi+1]
		h := Hunk{Ops: hop}
		for i := 0; i < lo; i++ {
			if ops[i].Kind != edit.Insert {
				h.OldStart++
			}
			if ops[i].Kind != edit.Delete {
				h.NewStart++
			}
		}
		for _, o := range hop {
			if o.Kind != edit.Insert {
				h.OldCount++
			}
			if o.Kind != edit.Delete {
				h.NewCount++
			}
		}
		if h.OldCount == 0 {
			// 纯插入：a 为插入点之前最后一行的 1 基行号，开头为 0。
		} else {
			h.OldStart++
		}
		if h.NewCount != 0 {
			h.NewStart++
		}
		out = append(out, h)
	}
	return out
}
