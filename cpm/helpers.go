package cpm

import (
	"container/heap"
	"sort"
)

type EdgeView struct {
	From int
	To   int
	Lag  int64
}

type Snapshot struct {
	Duration []int64
	SNET     []int64
	FNLT     []int64
	ES       []int64
	EF       []int64
	LS       []int64
	LF       []int64
	TF       []int64
	FF       []int64
	PF       int64
	Edges    []EdgeView
}

func (m *Maintainer) projectFinishLocked() int64 {
	if m.taskCount == 0 {
		return 0
	}
	return m.pfHeap[0].value
}

func (m *Maintainer) reachesLocked(from, to int) bool {
	if from == to {
		return true
	}
	queue := []int{from}
	seen := make([]bool, m.taskCount)
	seen[from] = true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, edge := range m.outEdges[u] {
			w := edge.to
			if w == to {
				return true
			}
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	return false
}

func (m *Maintainer) removeCandidate(candidates *[]*candidate, target *candidate) {
	m.removeCandidateAt(candidates, m.candidateIndex(candidates, target))
}

func (m *Maintainer) candidateIndex(candidates *[]*candidate, target *candidate) int {
	for i, item := range *candidates {
		if item == target {
			return i
		}
	}
	return -1
}

func (m *Maintainer) removeCandidateAt(candidates *[]*candidate, index int) {
	if index < 0 {
		return
	}
	last := len(*candidates) - 1
	(*candidates)[index] = (*candidates)[last]
	*candidates = (*candidates)[:last]
}

func (m *Maintainer) removeEdge(edges *[]*edgeState, target *edgeState) {
	for i, edge := range *edges {
		if edge == target {
			*edges = append((*edges)[:i], (*edges)[i+1:]...)
			return
		}
	}
}

func (m *Maintainer) replaceStaticStartLocked(v int, value int64) {
	for i := len(m.inCandidates[v]) - 1; i >= 0; i-- {
		item := m.inCandidates[v][i]
		if item.kind == kindStart && item.alive {
			item.alive = false
			break
		}
	}
	item := &candidate{value: value, kind: kindStart, src: -1, dst: v, alive: true}
	heap.Push(&m.inHeaps[v], item)
	m.inCandidates[v] = append(m.inCandidates[v], item)
}

func (m *Maintainer) replaceStaticFinishLocked(v int, value int64) {
	for i := len(m.outCandidates[v]) - 1; i >= 0; i-- {
		item := m.outCandidates[v][i]
		if item.kind == kindFinish && item.alive {
			item.alive = false
			break
		}
	}
	if value >= 0 {
		item := &candidate{value: value, kind: kindFinish, src: v, dst: -1, alive: true}
		heap.Push(&m.outHeaps[v], item)
		m.outCandidates[v] = append(m.outCandidates[v], item)
	}
}

func nonNilInts(values []int) []int {
	if values == nil {
		return []int{}
	}
	return values
}

func (m *Maintainer) cleanMaxLocked(v int) {
	for len(m.inHeaps[v]) > 0 && !m.inHeaps[v][0].alive {
		heap.Pop(&m.inHeaps[v])
	}
}

func (m *Maintainer) cleanMinLocked(v int) {
	for len(m.outHeaps[v]) > 0 && !m.outHeaps[v][0].alive {
		heap.Pop(&m.outHeaps[v])
	}
}

func sortedUnique(values []int) []int {
	if len(values) == 0 {
		return values
	}
	sort.Ints(values)
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] != values[read-1] {
			values[write] = values[read]
			write++
		}
	}
	return values[:write]
}

func (m *Maintainer) freeFloatLocked(v int) int64 {
	if len(m.outEdges[v]) == 0 {
		return m.projectFinishLocked() - m.ef[v]
	}
	result := int64(0)
	first := true
	for _, edge := range m.outEdges[v] {
		value := m.es[edge.to] - edge.lag - m.ef[v]
		if first || value < result {
			result = value
			first = false
		}
	}
	return result
}

func (m *Maintainer) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := m.taskCount
	snapshot := Snapshot{
		Duration: append([]int64(nil), m.duration[:n]...),
		SNET:     append([]int64(nil), m.snet[:n]...),
		FNLT:     append([]int64(nil), m.fnlt[:n]...),
		ES:       append([]int64(nil), m.es[:n]...),
		EF:       append([]int64(nil), m.ef[:n]...),
		LS:       append([]int64(nil), m.ls[:n]...),
		LF:       append([]int64(nil), m.lf[:n]...),
		TF:       append([]int64(nil), m.tf[:n]...),
		FF:       make([]int64, n),
		PF:       m.projectFinishLocked(),
	}
	for key, edge := range m.edges {
		snapshot.Edges = append(snapshot.Edges, EdgeView{From: key[0], To: key[1], Lag: edge.lag})
	}
	sort.Slice(snapshot.Edges, func(i, j int) bool {
		return snapshot.Edges[i].From < snapshot.Edges[j].From ||
			(snapshot.Edges[i].From == snapshot.Edges[j].From && snapshot.Edges[i].To < snapshot.Edges[j].To)
	})
	for v := 0; v < n; v++ {
		snapshot.FF[v] = m.freeFloatLocked(v)
	}
	return snapshot
}

func (m *Maintainer) Time(v int) (es, ef, ls, lf, tf, ff int64, critical bool, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if v < 0 || v >= m.taskCount {
		return 0, 0, 0, 0, 0, 0, false, ErrTaskNotFound
	}
	return m.es[v], m.ef[v], m.ls[v], m.lf[v], m.tf[v], m.freeFloatLocked(v), m.critical[v], nil
}

func (m *Maintainer) ProjectFinish() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.projectFinishLocked()
}

func (m *Maintainer) EvaluationCounts() (forward, backward int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fwdEval, m.bwdEval
}

func (m *Maintainer) Counts() (tasks, dependencies int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.taskCount, m.depCount
}
