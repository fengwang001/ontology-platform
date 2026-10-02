package ontology

import "sort"

func (m *Maintainer) AddEdges(edges [][2]int) (AddEdgesResult, error) {
	if len(edges) < 1 || len(edges) > 1000 {
		return AddEdgesResult{}, ErrInvalidBatchSize
	}

	requested := make([][2]int, len(edges))
	copy(requested, edges)

	m.mu.Lock()
	defer m.mu.Unlock()

	beforeOrd := append([]int(nil), m.ord[:m.created]...)
	beforeTouched := m.touched
	added := make([][2]int, 0, len(requested))
	counts := make([]EdgeCount, 0, len(requested))

	rollback := func() {
		for i := len(added) - 1; i >= 0; i-- {
			u, v := added[i][0], added[i][1]
			delete(m.out[u], v)
			delete(m.in[v], u)
			m.edgeCount--
		}
		copy(m.ord, beforeOrd)
		m.touched = beforeTouched
	}

	for i, edge := range requested {
		result, err := m.addEdgeLocked(edge[0], edge[1])
		if err != nil {
			rollback()
			return AddEdgesResult{}, &BatchError{Index: i, Reason: err}
		}
		counts = append(counts, EdgeCount{Forward: result.Forward, Backward: result.Backward})
		added = append(added, edge)
	}

	moved := make([]int, 0)
	for x := 0; x < m.created; x++ {
		if m.alive[x] && m.ord[x] != beforeOrd[x] {
			moved = append(moved, x)
		}
	}
	sort.Slice(moved, func(i, j int) bool {
		if m.ord[moved[i]] != m.ord[moved[j]] {
			return m.ord[moved[i]] < m.ord[moved[j]]
		}
		return moved[i] < moved[j]
	})

	return AddEdgesResult{Moved: moved, Counts: counts}, nil
}
