package ontology

import "sort"

func (m *Maintainer) AddEdge(u, v int) (AddEdgeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addEdgeLocked(u, v)
}

func (m *Maintainer) addEdgeLocked(u, v int) (AddEdgeResult, error) {

	if !m.live(u) || !m.live(v) {
		return AddEdgeResult{}, ErrNodeNotFound
	}
	if u == v {
		return AddEdgeResult{}, &CycleError{Witness: []int{u}}
	}
	if _, ok := m.out[u][v]; ok {
		return AddEdgeResult{}, ErrEdgeAlreadyExists
	}
	if m.edgeCount == m.edgeLimit {
		return AddEdgeResult{}, ErrEdgeLimit
	}
	if m.ord[u] < m.ord[v] {
		m.addEdgeUnlocked(u, v)
		return AddEdgeResult{}, nil
	}

	lb := m.ord[v]
	ub := m.ord[u]
	forward, parent := m.forwardSet(v, ub)
	if _, ok := forward[u]; ok {
		return AddEdgeResult{}, &CycleError{Witness: pathTo(parent, v, u)}
	}
	backward := m.backwardSet(u, lb)
	pool := make([]int, 0, len(forward)+len(backward))
	for x := range forward {
		pool = append(pool, m.ord[x])
	}
	for x := range backward {
		pool = append(pool, m.ord[x])
	}
	sort.Ints(pool)

	frontNodes := sortedNodes(backward, m.ord)
	frontNodes = append(frontNodes, sortedNodes(forward, m.ord)...)
	moved := make([]int, 0, len(frontNodes))
	for i, x := range frontNodes {
		old := m.ord[x]
		m.ord[x] = pool[i]
		if old != pool[i] {
			moved = append(moved, x)
		}
	}
	m.touched += len(frontNodes)
	sort.Slice(moved, func(i, j int) bool {
		if m.ord[moved[i]] != m.ord[moved[j]] {
			return m.ord[moved[i]] < m.ord[moved[j]]
		}
		return moved[i] < moved[j]
	})

	m.addEdgeUnlocked(u, v)
	return AddEdgeResult{
		Moved:    moved,
		Forward:  len(forward),
		Backward: len(backward),
	}, nil
}

func (m *Maintainer) RemoveEdge(u, v int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.live(u) || !m.live(v) {
		return ErrNodeNotFound
	}
	if _, ok := m.out[u][v]; !ok {
		return ErrEdgeNotFound
	}
	delete(m.out[u], v)
	delete(m.in[v], u)
	m.edgeCount--
	return nil
}

func (m *Maintainer) addEdgeUnlocked(u, v int) {
	m.out[u][v] = struct{}{}
	m.in[v][u] = struct{}{}
	m.edgeCount++
}

func (m *Maintainer) forwardSet(start, ub int) (map[int]struct{}, map[int]int) {
	seen := make(map[int]struct{})
	parent := make(map[int]int)
	queue := []int{start}
	seen[start] = struct{}{}
	parent[start] = start

	for head := 0; head < len(queue); head++ {
		layerEnd := len(queue)
		next := []int{}
		for ; head < layerEnd; head++ {
			x := queue[head]
			for y := range m.out[x] {
				if m.ord[y] > ub {
					continue
				}
				if _, ok := seen[y]; ok {
					continue
				}
				seen[y] = struct{}{}
				parent[y] = x
				next = append(next, y)
			}
		}
		sort.Ints(next)
		queue = append(queue, next...)
		head = layerEnd - 1
	}
	return seen, parent
}

func (m *Maintainer) backwardSet(start, lb int) map[int]struct{} {
	seen := make(map[int]struct{})
	queue := []int{start}
	seen[start] = struct{}{}

	for head := 0; head < len(queue); head++ {
		x := queue[head]
		for y := range m.in[x] {
			if m.ord[y] < lb {
				continue
			}
			if _, ok := seen[y]; ok {
				continue
			}
			seen[y] = struct{}{}
			queue = append(queue, y)
		}
	}
	return seen
}

func sortedNodes(set map[int]struct{}, ord []int) []int {
	nodes := make([]int, 0, len(set))
	for x := range set {
		nodes = append(nodes, x)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if ord[nodes[i]] != ord[nodes[j]] {
			return ord[nodes[i]] < ord[nodes[j]]
		}
		return nodes[i] < nodes[j]
	})
	return nodes
}

func pathTo(parent map[int]int, start, target int) []int {
	path := []int{}
	for x := target; x != start; x = parent[x] {
		path = append(path, x)
	}
	path = append(path, start)
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}
