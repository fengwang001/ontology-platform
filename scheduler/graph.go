package scheduler

import "slices"

func (s *Scheduler) edgeKey(u, v int) int64 {
	return int64(u)*int64(s.taskLimit+1) + int64(v)
}

func (s *Scheduler) edgeExists(u, v int) bool {
	_, ok := s.edges[s.edgeKey(u, v)]
	return ok
}

func (s *Scheduler) edgeValue(u, v int) (int64, bool) {
	lag, ok := s.edges[s.edgeKey(u, v)]
	return lag, ok
}

func (s *Scheduler) insertEdge(u, v int, lag int64) {
	s.edges[s.edgeKey(u, v)] = lag
	s.preds[v] = append(s.preds[v], u)
	s.succs[u] = append(s.succs[u], v)
}

func (s *Scheduler) deleteEdge(u, v int) {
	delete(s.edges, s.edgeKey(u, v))
	s.preds[v] = removeSorted(s.preds[v], u)
	s.succs[u] = removeSorted(s.succs[u], v)
}

func removeSorted(values []int, value int) []int {
	i := slices.Index(values, value)
	ok := i >= 0
	if !ok {
		return values
	}
	return append(values[:i], values[i+1:]...)
}

func (s *Scheduler) taskExists(v int) bool {
	return s.taskExistsLocked(v)
}

func (s *Scheduler) taskExistsLocked(v int) bool {
	return v >= 0 && v < s.taskCount
}

func (s *Scheduler) wouldCycle(u, v int) bool {
	if s.pos[u] < s.pos[v] {
		return false
	}
	l := s.pos[v]
	r := s.pos[u]
	reach := make([]bool, r-l+1)
	reach[0] = true
	for p := l; p <= r; p++ {
		if !reach[p-l] {
			continue
		}
		x := s.order[p]
		if x == u {
			return true
		}
		for _, w := range s.succs[x] {
			q := s.pos[w]
			if q >= l && q <= r {
				reach[q-l] = true
			}
		}
	}
	return false
}

func (s *Scheduler) moveAfter(u, v int) {
	l := s.pos[v]
	r := s.pos[u]
	if l > r {
		return
	}
	reach := make([]bool, r-l+1)
	reach[0] = true
	for p := l; p <= r; p++ {
		if !reach[p-l] {
			continue
		}
		x := s.order[p]
		for _, w := range s.succs[x] {
			q := s.pos[w]
			if q >= l && q <= r {
				reach[q-l] = true
			}
		}
	}
	remaining := make([]int, 0, r-l+1)
	block := make([]int, 0, r-l+1)
	for p := l; p <= r; p++ {
		x := s.order[p]
		if reach[p-l] {
			block = append(block, x)
		} else {
			remaining = append(remaining, x)
		}
	}
	merged := append(append(make([]int, 0, r-l+1), remaining...), block...)
	copy(s.order[l:r+1], merged)
	for p := l; p <= r; p++ {
		s.pos[s.order[p]] = p
	}
}
