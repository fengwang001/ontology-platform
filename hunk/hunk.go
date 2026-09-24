package hunk

import "ontology/edit"

type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []edit.Op
}

func Build(s edit.Script, context int) []Hunk {
	if context < 0 {
		context = 0
	}
	var changes []int
	for index, op := range s.Ops {
		if op.Kind != ' ' {
			changes = append(changes, index)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	groups := [][2]int{{{changes[0], changes[0]}}}
	for _, index := range changes[1:] {
		last := &groups[len(groups)-1]
		if index-last[1]-1 <= 2*context {
			last[1] = index
		} else {
			groups = append(groups, [2]int{index, index})
		}
	}
	hunks := make([]Hunk, 0, len(groups))
	for _, group := range groups {
		start := group[0] - context
		if start < 0 {
			start = 0
		}
		end := group[1] + context + 1
		if end > len(s.Ops) {
			end = len(s.Ops)
		}
		ops := append([]edit.Op(nil), s.Ops[start:end]...)
		h := Hunk{Ops: ops}
		oldBefore, newBefore := 0, 0
		for _, op := range s.Ops[:start] {
			if op.Kind != '+' {
				oldBefore++
			}
			if op.Kind != '-' {
				newBefore++
			}
		}
		for _, op := range ops {
			if op.Kind != '+' {
				h.OldCount++
			}
			if op.Kind != '-' {
				h.NewCount++
			}
		}
		h.OldStart, h.NewStart = oldBefore+1, newBefore+1
		if h.OldCount == 0 {
			h.OldStart = oldBefore
		}
		if h.NewCount == 0 {
			h.NewStart = newBefore
		}
		hunks = append(hunks, h)
	}
	return hunks
}
