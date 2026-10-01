package liveness

// naiveSolve is an intentionally simple round-by-round reimplementation of
// the exact specification: start every LiveIn/LiveOut at the empty set and
// repeatedly reapply the equations to all blocks until a full round changes
// nothing. Starting from empty and only growing yields the least fixpoint.
func naiveSolve(specs []BlockSpec) map[int][2][]string {
	g := make(map[int]BlockSpec, len(specs))
	ue := make(map[int]map[string]struct{}, len(specs))
	def := make(map[int]map[string]struct{}, len(specs))
	ids := make([]int, 0, len(specs))
	for _, s := range specs {
		g[s.ID] = s
		ids = append(ids, s.ID)
		u, d := blockSummary(s.Instructions)
		ue[s.ID] = u
		def[s.ID] = d
	}

	in := make(map[int]map[string]struct{}, len(ids))
	out := make(map[int]map[string]struct{}, len(ids))
	for _, id := range ids {
		in[id] = newSet()
		out[id] = newSet()
	}

	for {
		nextIn := make(map[int]map[string]struct{}, len(ids))
		nextOut := make(map[int]map[string]struct{}, len(ids))
		changed := false
		for _, id := range ids {
			no := newSet()
			for _, succ := range g[id].Successors {
				setUnion(no, in[succ])
			}
			ni := setCopy(ue[id])
			tmp := setCopy(no)
			setSubtract(tmp, def[id])
			setUnion(ni, tmp)
			nextIn[id] = ni
			nextOut[id] = no
			if !setEqual(ni, in[id]) || !setEqual(no, out[id]) {
				changed = true
			}
		}
		in, out = nextIn, nextOut
		if !changed {
			break
		}
	}

	result := make(map[int][2][]string, len(ids))
	for _, id := range ids {
		result[id] = [2][]string{sortedSlice(in[id]), sortedSlice(out[id])}
	}
	return result
}

func blockSummary(ins []Instruction) (ue, def map[string]struct{}) {
	ue = newSet()
	def = newSet()
	seenDef := newSet()
	for _, in := range ins {
		for _, v := range in.Uses {
			if !setContains(seenDef, v) {
				setAdd(ue, v)
			}
		}
		for _, v := range in.Defs {
			setAdd(seenDef, v)
			setAdd(def, v)
		}
	}
	return ue, def
}
