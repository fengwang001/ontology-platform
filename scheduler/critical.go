package scheduler

func (s *Scheduler) snapshotCritical() []int {
	nodes := []int{}
	for v := range s.critical {
		if s.critical[v] {
			nodes = append(nodes, v)
		}
	}
	return nodes
}

func (s *Scheduler) refreshCritical(old []int) ([]int, []int) {
	minTF, ok := s.currentMinTF()
	newSet := make([]bool, s.taskCount)
	if ok {
		s.minTF = minTF
	}
	for v := 0; v < s.taskCount; v++ {
		if ok && s.tf[v] == minTF {
			newSet[v] = true
		}
	}

	added := []int{}
	removed := []int{}
	for _, v := range old {
		if !newSet[v] {
			removed = append(removed, v)
		}
	}
	for v := range newSet {
		if newSet[v] && (v >= len(s.critical) || !s.critical[v]) {
			added = append(added, v)
		}
	}
	s.critical = newSet
	s.criticalCount = 0
	for _, isCritical := range newSet {
		if isCritical {
			s.criticalCount++
		}
	}
	return added, removed
}

func (s *Scheduler) refreshCriticalForAddTask(v int, oldMinTF int64, hadOld bool) ([]int, []int) {
	newTF := s.tf[v]
	added := []int{}
	removed := []int{}
	if !hadOld || newTF < oldMinTF {
		s.critical[v] = false
		for x := 0; x < v; x++ {
			if s.critical[x] {
				s.critical[x] = false
				removed = append(removed, x)
			}
		}
		s.minTF = newTF
		s.critical[v] = true
		s.criticalCount = 1
		added = append(added, v)
	} else if newTF == s.minTF {
		s.critical[v] = true
		s.criticalCount++
		added = append(added, v)
	}
	return added, removed
}

func (s *Scheduler) CriticalTasks() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotCritical()
}

func (s *Scheduler) CriticalPath() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	critical := s.snapshotCritical()
	if len(critical) == 0 {
		return []int{}
	}
	critSet := make([]bool, s.taskCount)
	for _, v := range critical {
		critSet[v] = true
	}

	start := -1
	for _, v := range critical {
		hasIn := false
		for _, u := range s.preds[v] {
			lag, _ := s.edgeValue(u, v)
			if critSet[u] && s.es[v] == s.ef[u]+lag {
				hasIn = true
			}
		}
		if !hasIn {
			start = v
			break
		}
	}

	path := []int{}
	if start < 0 {
		return path
	}
	visited := make([]bool, s.taskCount)
	current := start
	for current >= 0 && !visited[current] {
		visited[current] = true
		path = append(path, current)
		next := -1
		for _, w := range s.succs[current] {
			if critSet[w] {
				lag, _ := s.edgeValue(current, w)
				if s.es[w] == s.ef[current]+lag {
					if next < 0 || w < next {
						next = w
					}
				}
			}
		}
		current = next
	}
	return path
}
