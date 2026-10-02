package ontology

import "sort"

func (m *IncrementalBridges) IsBridge(id int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || id >= len(m.edges) {
		return false, ErrEdgeNotFound
	}
	return m.edges[id].bridge, nil
}

func (m *IncrementalBridges) Block(x int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x < 0 || x >= m.n {
		return 0, ErrInvalidNode
	}
	return m.label[m.findBlock(x)], nil
}

func (m *IncrementalBridges) Connected(u, v int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u < 0 || u >= m.n || v < 0 || v >= m.n {
		return false, ErrInvalidNode
	}
	return m.findComponent(u) == m.findComponent(v), nil
}

func (m *IncrementalBridges) BridgeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bridges
}

func (m *IncrementalBridges) BlockCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blocks
}

func (m *IncrementalBridges) ComponentCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.countRoots(m.parent)
}

func (m *IncrementalBridges) countRoots(parents []int) int {
	count := 0
	for i := range parents {
		x := i
		for parents[x] != x {
			x = parents[x]
		}
		if x == i {
			count++
		}
	}
	return count
}

func (m *IncrementalBridges) BridgesOnPath(u, v int) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u < 0 || u >= m.n || v < 0 || v >= m.n {
		return nil, ErrInvalidNode
	}
	if m.findComponent(u) != m.findComponent(v) {
		return nil, ErrNotConnected
	}
	ids := m.pathBridges(u, v)
	sort.Ints(ids)
	return ids, nil
}

func (m *IncrementalBridges) MinBridge(u, v int) (BridgeWeight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u < 0 || u >= m.n || v < 0 || v >= m.n {
		return BridgeWeight{}, ErrInvalidNode
	}
	if m.findComponent(u) != m.findComponent(v) {
		return BridgeWeight{}, ErrNotConnected
	}
	un := m.vertexNode(u)
	vn := m.vertexNode(v)
	m.evert(un)
	m.access(vn)
	value := m.lctMin[vn]
	if value.empty {
		return BridgeWeight{}, ErrNoBridgeOnPath
	}
	return BridgeWeight{ID: value.id, Weight: value.weight}, nil
}

func (m *IncrementalBridges) BlockWeight(x int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x < 0 || x >= m.n {
		return 0, ErrInvalidNode
	}
	return m.blockWeight[m.findBlock(x)], nil
}

func (m *IncrementalBridges) EdgeHistory(id int) (bornVersion, unbridgedVersion int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || id >= len(m.edges) {
		return 0, 0, ErrEdgeNotFound
	}
	rec := m.edges[id]
	return rec.born, rec.unbridged, nil
}

func (m *IncrementalBridges) Steps() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.steps
}
