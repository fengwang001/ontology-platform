package scheduler

import "slices"

func (s *Scheduler) forwardSeeds(seeds []int) []int {
	s.fwdEval = 0
	queue := append([]int{}, seeds...)
	queued := make([]bool, s.taskCount)
	for _, v := range queue {
		queued[v] = true
	}
	changed := []int{}

	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		queued[v] = false
		s.fwdEval++

		newES := s.computeES(v)
		if newES == s.es[v] {
			continue
		}
		s.es[v] = newES
		s.ef[v] = newES + s.duration[v]
		changed = append(changed, v)
		for _, w := range s.succs[v] {
			if !queued[w] {
				queue = append(queue, w)
				queued[w] = true
			}
		}
	}
	slices.Sort(changed)
	return changed
}

func (s *Scheduler) forwardChanged(forced int) []int {
	s.fwdEval = 0
	queue := []int{}
	queued := make([]bool, s.taskCount)
	changed := []int{}
	for _, w := range s.succs[forced] {
		queue = append(queue, w)
		queued[w] = true
	}

	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		queued[v] = false
		s.fwdEval++

		newES := s.computeES(v)
		if newES == s.es[v] {
			continue
		}
		s.es[v] = newES
		s.ef[v] = newES + s.duration[v]
		changed = append(changed, v)
		for _, w := range s.succs[v] {
			if !queued[w] {
				queue = append(queue, w)
				queued[w] = true
			}
		}
	}
	slices.Sort(changed)
	return changed
}

func (s *Scheduler) backwardSeeds(seeds []int) []int {
	s.bwdEval = 0
	queue := append([]int{}, seeds...)
	queued := make([]bool, s.taskCount)
	for _, v := range queue {
		queued[v] = true
	}
	changed := []int{}

	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		queued[v] = false
		s.bwdEval++

		newLF := s.computeLF(v)
		if newLF == s.lf[v] {
			continue
		}
		s.lf[v] = newLF
		s.ls[v] = newLF - s.duration[v]
		changed = append(changed, v)
		for _, u := range s.preds[v] {
			if !queued[u] {
				queue = append(queue, u)
				queued[u] = true
			}
		}
	}
	slices.Sort(changed)
	return changed
}

func (s *Scheduler) backwardChanged(forced int) []int {
	s.bwdEval = 0
	queue := []int{}
	queued := make([]bool, s.taskCount)
	changed := []int{}
	for _, u := range s.preds[forced] {
		queue = append(queue, u)
		queued[u] = true
	}

	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		queued[v] = false
		s.bwdEval++

		newLF := s.computeLF(v)
		if newLF == s.lf[v] {
			continue
		}
		s.lf[v] = newLF
		s.ls[v] = newLF - s.duration[v]
		changed = append(changed, v)
		for _, u := range s.preds[v] {
			if !queued[u] {
				queue = append(queue, u)
				queued[u] = true
			}
		}
	}
	slices.Sort(changed)
	return changed
}

func (s *Scheduler) computeES(v int) int64 {
	value := s.snet[v]
	for _, u := range s.preds[v] {
		lag, _ := s.edgeValue(u, v)
		candidate := s.ef[u] + lag
		if candidate > value {
			value = candidate
		}
	}
	return value
}

func (s *Scheduler) computeLF(v int) int64 {
	value := s.deadline
	if s.fnlt[v] >= 0 && s.fnlt[v] < value {
		value = s.fnlt[v]
	}
	for _, w := range s.succs[v] {
		lag, _ := s.edgeValue(v, w)
		candidate := s.ls[w] - lag
		if candidate < value {
			value = candidate
		}
	}
	return value
}

func (s *Scheduler) refreshTFs(nodes []int) {
	for _, v := range uniqueSorted(nodes) {
		newTF := s.lf[v] - s.ef[v]
		if s.tf[v] != newTF {
			s.removeTFCount(v)
			s.tf[v] = newTF
			s.tfCount[newTF]++
			if newTF < s.minTF {
				s.minTF = newTF
			}
		}
	}
}

func (s *Scheduler) removeTFCount(v int) {
	oldTF := s.tf[v]
	s.tfCount[oldTF]--
	if s.tfCount[oldTF] == 0 {
		delete(s.tfCount, oldTF)
	}
}

func (s *Scheduler) currentPF() int64 {
	return s.pf
}

func (s *Scheduler) updatePF(touched []int) {
	maxTouched := int64(0)
	oldStillExists := false
	for _, v := range touched {
		if s.ef[v] > maxTouched {
			maxTouched = s.ef[v]
		}
		if s.ef[v] == s.pf {
			oldStillExists = true
		}
	}
	if oldStillExists {
		if maxTouched > s.pf {
			s.pf = maxTouched
		}
		return
	}
	maxPF := maxTouched
	for v := 0; v < s.taskCount; v++ {
		if s.ef[v] > maxPF {
			maxPF = s.ef[v]
		}
	}
	s.pf = maxPF
}

func (s *Scheduler) currentMinTF() (int64, bool) {
	if s.taskCount == 0 {
		return 0, false
	}
	if _, ok := s.tfCount[s.minTF]; ok {
		return s.minTF, true
	}
	return s.minimumFromTFCount()
}

func (s *Scheduler) minimumFromTFCount() (int64, bool) {
	minTF := int64(0)
	first := true
	for value := range s.tfCount {
		if first || value < minTF {
			minTF = value
			first = false
		}
	}
	return minTF, !first
}

func mergeInts(groups ...[]int) []int {
	merged := []int{}
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return uniqueSorted(merged)
}

func uniqueSorted(values []int) []int {
	slices.Sort(values)
	return slices.Compact(values)
}

func nonNilSorted(values []int) []int {
	if values == nil {
		return []int{}
	}
	return values
}
