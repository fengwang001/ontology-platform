package engine

import "ontology/model"

// closure computes the transitive closure of the effective graph as bitsets.
func closure(out [][]int, n int) []uint64 {
	reach := make([]uint64, n+1)
	for v := 1; v <= n; v++ {
		for _, w := range out[v] {
			reach[v] |= 1 << uint(w-1)
		}
	}
	for changed := true; changed; {
		changed = false
		for v := 1; v <= n; v++ {
			for _, w := range out[v] {
				if reach[v]|reach[w] != reach[v] {
					reach[v] |= reach[w]
					changed = true
				}
			}
		}
	}
	return reach
}

// arrive delivers a token travelling edge u->v to node v.
func (in *Instance) arrive(u, v int) {
	switch in.kind[v] {
	case model.Task:
		in.act[v]++
	case model.AndSplit, model.XorSplit, model.OrSplit:
		for _, w := range in.out[v] {
			in.arrive(v, w)
		}
	case model.End:
		in.end++
	default: // joins
		in.arr[[2]int{u, v}]++
	}
}

// settle repeatedly scans joins ascending by id until nothing fires.
func (in *Instance) settle() {
	for {
		fired := false
		for _, j := range in.joins {
			if in.kind[j] == model.AndJoin {
				if !in.andReady(j) {
					continue
				}
				for _, u := range in.pred[j] {
					k := [2]int{u, j}
					if in.arr[k]--; in.arr[k] == 0 {
						delete(in.arr, k)
					}
				}
			} else {
				if in.arrSum(j) == 0 || in.blocked(j) {
					continue
				}
				for _, u := range in.pred[j] {
					delete(in.arr, [2]int{u, j})
				}
			}
			in.fires[j]++
			in.arrive(j, in.out[j][0])
			fired = true
		}
		if !fired {
			return
		}
	}
}

func (in *Instance) andReady(j int) bool {
	for _, u := range in.pred[j] {
		if in.arr[[2]int{u, j}] == 0 {
			return false
		}
	}
	return true
}

func (in *Instance) arrSum(j int) int {
	s := 0
	for _, u := range in.pred[j] {
		s += in.arr[[2]int{u, j}]
	}
	return s
}

// blocked reports whether any other live token can reach j on the effective
// graph. Only token-holding nodes are examined, never the whole graph.
func (in *Instance) blocked(j int) bool {
	bit := uint64(1) << uint(j-1)
	for t := range in.act {
		if in.reach[t]&bit != 0 {
			return true
		}
	}
	for _, o := range in.joins {
		if o != j && in.arrSum(o) > 0 && in.reach[o]&bit != 0 {
			return true
		}
	}
	return false
}
