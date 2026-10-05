package cpm

import (
	"container/heap"
	"sort"
)

func (m *Maintainer) updateLocked(fwdRoots, bwdRoots []int, fwdForceEdges, bwdForceEdges map[int]struct{}, forceTasks map[int]struct{}, durationChanged bool, oldPF int64) *UpdateReport {
	oldMin := int64(0)
	if len(m.tfHeap) > 0 {
		oldMin = m.tfHeap[0].value
	}
	hadTasks := len(m.tfHeap) > 0
	m.fwdEval = 0
	m.bwdEval = 0

	durationRoots := map[int]struct{}{}
	if durationChanged {
		for _, v := range bwdRoots {
			durationRoots[v] = struct{}{}
		}
		bwdRoots = nil
	}
	changedLF := m.backwardTwoRootsLocked(bwdRoots, bwdForceEdges, forceTasks)
	changedES := m.forwardLocked(fwdRoots, fwdForceEdges, forceTasks)
	if durationChanged {
		secondBWD := m.backwardDurationLocked(sortedUnique(append([]int(nil), changedES...)), fwdRoots)
		changedLF = sortedUnique(append(changedLF, secondBWD...))
	}
	if len(fwdForceEdges) > 0 && (len(changedES) > 0 || len(changedLF) > 0) {
		secondRoots := append(append([]int(nil), changedES...), bwdRoots...)
		secondRoots = append(secondRoots, fwdRoots...)
		secondForce := map[int]struct{}{}
		for v := range durationRoots {
			secondForce[v] = struct{}{}
		}
		secondBWD := m.backwardLocked(sortedUnique(secondRoots), map[int]struct{}{}, secondForce)
		changedLF = sortedUnique(append(changedLF, secondBWD...))
		secondFWD := m.forwardLocked(changedLF, nil, nil)
		changedES = sortedUnique(append(changedES, secondFWD...))
	}
	if len(bwdForceEdges) > 0 {
		thirdRoots := append([]int(nil), changedLF...)
		for v := range bwdForceEdges {
			thirdRoots = append(thirdRoots, v)
		}
		thirdBWD := m.backwardLocked(sortedUnique(thirdRoots), nil, bwdForceEdges)
		changedLF = sortedUnique(append(changedLF, thirdBWD...))
	}
	affected := map[int]struct{}{}
	for _, roots := range [][]int{fwdRoots, bwdRoots, changedES, changedLF} {
		for _, v := range roots {
			affected[v] = struct{}{}
		}
	}
	for v := range affected {
		m.setTFLocked(v, m.lf[v]-m.ef[v])
	}
	if !hadTasks {
		for v := 0; v < m.taskCount; v++ {
			if _, exists := affected[v]; !exists {
				m.setTFLocked(v, m.lf[v]-m.ef[v])
			}
		}
	}

	newPF := int64(0)
	if m.taskCount > 0 {
		newPF = m.pfHeap[0].value
	}
	minTF := int64(0)
	if m.taskCount > 0 {
		minTF = m.tfHeap[0].value
	}

	added := []int{}
	removed := []int{}
	if minTF != oldMin {
		for _, v := range m.criticalList {
			if m.tf[v] != minTF {
				removed = append(removed, v)
			}
		}
		if m.taskCount > 0 {
			for v := range m.tfBuckets[minTF] {
				if !m.critical[v] {
					added = append(added, v)
				}
			}
		}
	} else if m.taskCount > 0 {
		for _, v := range m.criticalList {
			if m.tf[v] != minTF {
				removed = append(removed, v)
			}
		}
		for v := range affected {
			if m.tf[v] == minTF && !m.critical[v] {
				added = append(added, v)
			}
		}
	}
	sort.Ints(added)
	sort.Ints(removed)
	for _, v := range removed {
		m.critical[v] = false
	}
	for _, v := range added {
		m.critical[v] = true
	}
	for _, v := range removed {
		if index := sort.SearchInts(m.criticalList, v); index < len(m.criticalList) && m.criticalList[index] == v {
			m.criticalList = append(m.criticalList[:index], m.criticalList[index+1:]...)
		}
	}
	for _, v := range added {
		index := sort.SearchInts(m.criticalList, v)
		m.criticalList = append(m.criticalList, 0)
		copy(m.criticalList[index+1:], m.criticalList[index:])
		m.criticalList[index] = v
	}

	return &UpdateReport{
		ChangedES:   nonNilInts(changedES),
		ChangedLF:   nonNilInts(changedLF),
		OldPF:       oldPF,
		NewPF:       newPF,
		CritAdded:   nonNilInts(added),
		CritRemoved: nonNilInts(removed),
	}
}

func (m *Maintainer) backwardTwoRootsLocked(roots []int, forceEdges, forceTasks map[int]struct{}) []int {
	if len(roots) <= 1 {
		return m.backwardLocked(roots, forceEdges, forceTasks)
	}
	changed := m.backwardLocked(roots[1:], nil, forceTasks)
	secondChanged := m.backwardLocked(roots[:1], nil, nil)
	return sortedUnique(append(changed, secondChanged...))
}

func (m *Maintainer) freshBackwardLocked(roots []int, forceEdges, forceTasks map[int]struct{}) []int {
	return m.backwardLocked(roots, forceEdges, forceTasks)
}

func (m *Maintainer) backwardDurationLocked(seeds, durationRoots []int) []int {
	closure := map[int]struct{}{}
	queue := append(append([]int(nil), seeds...), durationRoots...)
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if _, seen := closure[v]; seen {
			continue
		}
		closure[v] = struct{}{}
		for _, edge := range m.inEdges[v] {
			queue = append(queue, edge.from)
		}
	}
	remaining := make([]int, len(m.duration))
	for u := range closure {
		for _, edge := range m.outEdges[u] {
			if _, relevant := closure[edge.to]; relevant {
				remaining[u]++
			}
		}
	}
	ready := []int{}
	for v := range closure {
		if remaining[v] == 0 {
			ready = append(ready, v)
		}
	}
	durationSet := mapIntSet(durationRoots)
	_ = durationSet
	changed := []int{}
	for len(ready) > 0 {
		sort.Ints(ready)
		v := ready[0]
		ready = ready[1:]
		m.cleanMinLocked(v)
		_, forced := durationSet[v]
		if !forced && m.outHeaps[v][0].value == m.lf[v] && m.outHeaps[v][0].value-m.duration[v] == m.ls[v] {
			for _, edge := range m.inEdges[v] {
				u := edge.from
				if _, relevant := closure[u]; relevant {
					remaining[u]--
					if remaining[u] == 0 {
						ready = append(ready, u)
					}
				}
			}
			continue
		}
		m.bwdEval++
		newLF := m.outHeaps[v][0].value
		newLS := newLF - m.duration[v]
		oldLF, oldLS := m.lf[v], m.ls[v]
		m.lf[v], m.ls[v] = newLF, newLS
		if newLF != oldLF {
			changed = append(changed, v)
		}
		if newLS != oldLS {
			for _, edge := range m.inEdges[v] {
				u := edge.from
				updated := &candidate{value: newLS - edge.lag, edge: true, alive: true, src: u, dst: v, kind: kindEdge}
				edge.bwd.alive = false
				edge.bwd = updated
				edge.bwdVersions = append(edge.bwdVersions, updated)
				heap.Push(&m.outHeaps[u], updated)
				m.outCandidates[u] = append(m.outCandidates[u], updated)
				m.cleanMinLocked(u)
			}
		}
		for _, edge := range m.inEdges[v] {
			u := edge.from
			if _, relevant := closure[u]; relevant {
				remaining[u]--
				if remaining[u] == 0 {
					ready = append(ready, u)
				}
			}
		}
	}
	return sortedUnique(changed)
}

func mapIntSet(values []int) map[int]struct{} {
	result := map[int]struct{}{}
	for _, v := range values {
		result[v] = struct{}{}
	}
	return result
}

func (m *Maintainer) forwardLocked(roots []int, forceEdges, forceTasks map[int]struct{}) []int {
	active := map[int]struct{}{}
	for _, v := range roots {
		active[v] = struct{}{}
	}
	visited := map[int]struct{}{}
	changed := []int{}

	for len(active) > 0 {
		wave := mapKeysSorted(active)
		dirty := map[int]bool{}
		previous := visited
		visited = map[int]struct{}{}
		for v := range previous {
			visited[v] = struct{}{}
		}
		for _, v := range wave {
			dirty[v] = true
		}
		next := map[int]struct{}{}

		for _, v := range wave {
			if _, done := visited[v]; done {
				continue
			}
			blocked := false
			for _, edge := range m.inEdges[v] {
				u := edge.from
				if _, pending := active[u]; pending {
					blocked = true
					break
				}
			}
			if blocked {
				next[v] = struct{}{}
				delete(visited, v)
				continue
			}
			if _, done := visited[v]; done {
				continue
			}
			m.cleanMaxLocked(v)
			_, forcedTask := forceTasks[v]
			if !forcedTask && m.inHeaps[v][0].value == m.es[v] && m.pfItems[v] != nil {
				visited[v] = struct{}{}
				continue
			}
			m.fwdEval++
			visited[v] = struct{}{}
			newES := m.inHeaps[v][0].value
			newEF := newES + m.duration[v]
			oldES := m.es[v]
			oldEF := m.ef[v]
			isNew := m.pfItems[v] == nil
			m.es[v] = newES
			m.ef[v] = newEF
			if newES != oldES || isNew {
				changed = append(changed, v)
			}

			_, force := forceEdges[v]
			if force || newEF != oldEF || isNew {
				for _, edge := range m.outEdges[v] {
					w := edge.to
					old := edge.fwd
					old.alive = false
					updated := &candidate{value: newEF + edge.lag, edge: true, alive: true, src: v, dst: w, kind: kindEdge}
					edge.fwd = updated
					edge.fwdVersions = append(edge.fwdVersions, updated)
					heap.Push(&m.inHeaps[w], updated)
					m.inCandidates[w] = append(m.inCandidates[w], updated)
					m.cleanMaxLocked(w)
					if m.inHeaps[w][0].value != m.es[w] {
						dirty[w] = true
					}
				}
			}

			item := m.pfItems[v]
			if item == nil {
				item = &valueItem{task: v, value: newEF}
				m.pfItems[v] = item
				heap.Push(&m.pfHeap, item)
			} else if item.value != newEF {
				item.value = newEF
				heap.Fix(&m.pfHeap, item.index)
			}
		}
		for v := range previous {
			if _, queuedThisWave := next[v]; !queuedThisWave {
				delete(dirty, v)
			}
		}

		for w, needsEval := range dirty {
			if needsEval {
				next[w] = struct{}{}
			}
		}
		active = next
	}

	sort.Ints(changed)
	return sortedUnique(changed)
}

func (m *Maintainer) backwardLocked(roots []int, forceEdges, forceTasks map[int]struct{}) []int {
	active := map[int]struct{}{}
	for _, v := range roots {
		active[v] = struct{}{}
	}
	visited := map[int]struct{}{}
	changed := []int{}
	for len(active) > 0 {
		wave := mapKeysSorted(active)
		dirty := map[int]bool{}
		previous := visited
		visited = map[int]struct{}{}
		for v := range previous {
			visited[v] = struct{}{}
		}
		for _, v := range wave {
			dirty[v] = true
		}
		next := map[int]struct{}{}

		for _, v := range wave {
			if _, done := visited[v]; done {
				continue
			}
			m.cleanMinLocked(v)
			_, forcedTask := forceTasks[v]
			if forcedTask {
				for _, edge := range m.outEdges[v] {
					if edge.bwd != nil {
						continue
					}
					updated := &candidate{value: m.ls[edge.to] - edge.lag, edge: true, alive: true, src: v, dst: edge.to, kind: kindEdge}
					edge.bwd = updated
					edge.bwdVersions = append(edge.bwdVersions, updated)
					heap.Push(&m.outHeaps[v], updated)
					m.outCandidates[v] = append(m.outCandidates[v], updated)
				}
				m.cleanMinLocked(v)
			}
			if !forcedTask && m.outHeaps[v][0].value == m.lf[v] && m.pfItems[v] != nil {
				visited[v] = struct{}{}
				continue
			}
			m.bwdEval++
			visited[v] = struct{}{}
			newLF := m.outHeaps[v][0].value
			newLS := newLF - m.duration[v]
			oldLF := m.lf[v]
			oldLS := m.ls[v]
			isNew := m.pfItems[v] == nil
			m.lf[v] = newLF
			m.ls[v] = newLS
			if newLF != oldLF || isNew {
				changed = append(changed, v)
			}

			_, force := forceEdges[v]
			if force || newLS != oldLS || isNew {
				for _, edge := range m.inEdges[v] {
					u := edge.from
					updated := &candidate{value: newLS - edge.lag, edge: true, alive: true, src: u, dst: v, kind: kindEdge}
					if edge.bwd != nil {
						edge.bwd.alive = false
					}
					edge.bwd = updated
					edge.bwdVersions = append(edge.bwdVersions, updated)
					heap.Push(&m.outHeaps[u], updated)
					m.outCandidates[u] = append(m.outCandidates[u], updated)
					m.cleanMinLocked(u)
					dirty[u] = true
				}
			}
		}
		for v := range previous {
			if _, blocked := next[v]; !blocked {
				delete(dirty, v)
			}
		}

		for u, needsEval := range dirty {
			if needsEval {
				next[u] = struct{}{}
			}
		}
		active = next
	}

	sort.Ints(changed)
	return sortedUnique(changed)
}

func mapKeysSorted(values map[int]struct{}) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func (m *Maintainer) setTFLocked(v int, value int64) {
	if item := m.tfItems[v]; item != nil {
		if bucket := m.tfBuckets[item.value]; bucket != nil {
			delete(bucket, v)
			if len(bucket) == 0 {
				delete(m.tfBuckets, item.value)
			}
		}
		item.value = value
		heap.Fix(&m.tfHeap, item.index)
	} else {
		item := &valueItem{task: v, value: value}
		m.tfItems[v] = item
		heap.Push(&m.tfHeap, item)
	}
	m.tf[v] = value
	bucket := m.tfBuckets[value]
	if bucket == nil {
		bucket = map[int]struct{}{}
		m.tfBuckets[value] = bucket
	}
	bucket[v] = struct{}{}
}
