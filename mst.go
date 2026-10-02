package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArguments = errors.New("invalid arguments")
	ErrEdgeLimit        = errors.New("edge limit reached")
	ErrEdgeNotFound     = errors.New("edge not found")
	ErrSameNode         = errors.New("path endpoints are the same node")
	ErrNotConnected     = errors.New("nodes are not connected")
)

type UpdateResult struct {
	Version    int
	Entered    []int
	Left       []int
	Weight     int64
	Components int
}

type PathEdge struct {
	ID     int
	Weight int64
}

type DynamicMSF struct {
	mu sync.RWMutex

	n             int
	maxEdges      int
	version       int
	nextID        int
	activeEdges   int
	treeEdges     int
	totalWeight   int64
	components    int
	scanned       int
	edges         map[int]edgeState
	fullAdj       []map[int]struct{}
	forestAdj     []map[int]struct{}
	sideMark      []int
	sideStamp     int
	component     []int
	nextComponent int
	componentSize map[int]int
	candidateMark map[int]bool
}

type edgeState struct {
	u        int
	v        int
	weight   int64
	alive    bool
	inForest bool
	since    int
}

func NewDynamicMSF(n, maxEdges int) (*DynamicMSF, error) {
	if n < 1 || n > 100000 || maxEdges < 1 || maxEdges > 500000 {
		return nil, ErrInvalidArguments
	}
	m := &DynamicMSF{}
	fullAdj := make([]map[int]struct{}, n)
	forestAdj := make([]map[int]struct{}, n)
	for i := range fullAdj {
		fullAdj[i] = make(map[int]struct{})
		forestAdj[i] = make(map[int]struct{})
	}
	m = &DynamicMSF{
		n:          n,
		maxEdges:   maxEdges,
		components: n,
		nextID:     1,
		edges:      make(map[int]edgeState),
		fullAdj:    fullAdj,
		forestAdj:  forestAdj,
		sideMark:   make([]int, n),
		component: func() []int {
			labels := make([]int, n)
			for i := range labels {
				labels[i] = i
			}
			return labels
		}(),
		nextComponent: n,
		componentSize: map[int]int{},
		candidateMark: make(map[int]bool),
	}
	for i := 0; i < n; i++ {
		m.componentSize[i] = 1
	}
	return m, nil
}

func (m *DynamicMSF) AddEdge(u, v int, w int64) (int, UpdateResult, error) {
	if !m.validNode(u) || !m.validNode(v) || u == v || !validWeight(w) {
		return 0, UpdateResult{}, ErrInvalidArguments
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.activeEdges == m.maxEdges {
		return 0, UpdateResult{}, ErrEdgeLimit
	}

	id := m.nextID
	ed := edgeState{u: u, v: v, weight: w, alive: true}
	m.edges[id] = ed
	m.nextID++
	m.activeEdges++
	m.fullAdj[u][id] = struct{}{}
	m.fullAdj[v][id] = struct{}{}

	result := UpdateResult{Entered: []int{}, Left: []int{}, Components: m.components}
	m.version++
	if m.forestConnected(u, v) {
		maxID, maxWeight := m.forestPathMax(u, v)
		if edgeLess(w, id, maxWeight, maxID) {
			m.leaveForest(maxID)
			m.enterForest(id)
			result.Entered = []int{id}
			result.Left = []int{maxID}
		}
	} else {
		m.enterForest(id)
		result.Entered = []int{id}
	}

	result.Version = m.version
	result.Weight = m.totalWeight
	result.Components = m.components
	return id, result, nil
}

func (m *DynamicMSF) SetWeight(id int, w int64) (UpdateResult, error) {
	if !validID(id) || !validWeight(w) {
		return UpdateResult{}, ErrInvalidArguments
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	ed, ok := m.edges[id]
	if !ok || !ed.alive {
		return UpdateResult{}, ErrEdgeNotFound
	}

	oldWeight := ed.weight
	ed.weight = w
	m.edges[id] = ed
	if ed.inForest {
		m.totalWeight += w - oldWeight
	}
	m.version++

	result := UpdateResult{
		Version:    m.version,
		Entered:    []int{},
		Left:       []int{},
		Weight:     m.totalWeight,
		Components: m.components,
	}

	if ed.inForest {
		if w < oldWeight {
			return result, nil
		}

		candidate := m.findReplacement(ed.u, ed.v, id, true)
		if candidate != id {
			m.leaveForest(id)
			m.enterForest(candidate)
			result.Entered = []int{candidate}
			result.Left = []int{id}
		} else {
		}
		result.Weight = m.totalWeight
		result.Components = m.components
		return result, nil
	}

	if m.forestConnected(ed.u, ed.v) {
		maxID, maxWeight := m.forestPathMax(ed.u, ed.v)
		if edgeLess(w, id, maxWeight, maxID) {
			m.leaveForest(maxID)
			m.enterForest(id)
			result.Entered = []int{id}
			result.Left = []int{maxID}
		}
	}
	result.Weight = m.totalWeight
	result.Components = m.components
	return result, nil
}

func (m *DynamicMSF) RemoveEdge(id int) (UpdateResult, error) {
	if !validID(id) {
		return UpdateResult{}, ErrEdgeNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	ed, ok := m.edges[id]
	if !ok || !ed.alive {
		return UpdateResult{}, ErrEdgeNotFound
	}

	m.version++
	u, v := ed.u, ed.v
	delete(m.fullAdj[u], id)
	delete(m.fullAdj[v], id)

	result := UpdateResult{
		Version:    m.version,
		Entered:    []int{},
		Left:       []int{},
		Weight:     m.totalWeight,
		Components: m.components,
	}

	if ed.inForest {
		m.leaveForest(id)

		candidate := m.findReplacement(u, v, id, false)
		if candidate != 0 {
			m.enterForest(candidate)
			result.Entered = []int{candidate}
		}
		result.Left = []int{id}
	}

	delete(m.edges, id)
	m.activeEdges--
	result.Weight = m.totalWeight
	result.Components = m.components
	return result, nil
}

func (m *DynamicMSF) InForest(id int) (bool, error) {
	if !validID(id) {
		return false, ErrEdgeNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ed, ok := m.edges[id]
	if !ok || !ed.alive {
		return false, ErrEdgeNotFound
	}
	return ed.inForest, nil
}

func (m *DynamicMSF) TreeSince(id int) (int, error) {
	if !validID(id) {
		return 0, ErrEdgeNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ed, ok := m.edges[id]
	if !ok || !ed.alive {
		return 0, ErrEdgeNotFound
	}
	return ed.since, nil
}

func (m *DynamicMSF) Weight() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalWeight
}

func (m *DynamicMSF) Components() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.components
}

func (m *DynamicMSF) Connected(u, v int) (bool, error) {
	if !m.validNode(u) || !m.validNode(v) {
		return false, ErrInvalidArguments
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.forestConnected(u, v), nil
}

func (m *DynamicMSF) PathMax(u, v int) (PathEdge, error) {
	if !m.validNode(u) || !m.validNode(v) {
		return PathEdge{}, ErrInvalidArguments
	}
	if u == v {
		return PathEdge{}, ErrSameNode
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.forestConnected(u, v) {
		return PathEdge{}, ErrNotConnected
	}
	id, weight := m.forestPathMax(u, v)
	return PathEdge{ID: id, Weight: weight}, nil
}

func (m *DynamicMSF) validNode(node int) bool {
	return node >= 0 && node < m.n
}

func validWeight(weight int64) bool {
	return weight >= -1_000_000_000 && weight <= 1_000_000_000
}

func validID(id int) bool {
	return id >= 1
}

func edgeLess(weightA int64, idA int, weightB int64, idB int) bool {
	return weightA < weightB || (weightA == weightB && idA < idB)
}

func (m *DynamicMSF) enterForest(id int) {
	ed := m.edges[id]
	ed.inForest = true
	ed.since = m.version
	m.edges[id] = ed
	m.forestLink(id)
	m.totalWeight += ed.weight
	m.treeEdges++
	m.components--
}

func (m *DynamicMSF) leaveForest(id int) {
	ed := m.edges[id]
	ed.inForest = false
	ed.since = 0
	m.edges[id] = ed
	m.forestCut(id)
	m.totalWeight -= ed.weight
	m.treeEdges--
	m.components++
}

func (m *DynamicMSF) findReplacement(u, v, skipID int, allowSelf bool) int {
	m.sideStamp++
	m.scanned = 0
	mark := m.sideMark
	queueA := []int{u}
	queueB := []int{v}
	pendingA := make([]int, 0, 8)
	pendingB := make([]int, 0, 8)
	mark[u] = m.sideStamp
	mark[v] = -m.sideStamp

	bestID := 0
	var best edgeState
	if allowSelf {
		bestID = skipID
		best = m.edges[skipID]
	}
	consider := func(id int) {
		if m.candidateMark[id] || id == skipID {
			return
		}
		m.candidateMark[id] = true
		ed := m.edges[id]
		if bestID == 0 || edgeLess(ed.weight, id, best.weight, bestID) {
			bestID = id
			best = ed
		}
	}

	scanNode := func(node, nodeColor int, queue []int, pending *[]int) []int {
		m.scanned++
		for id := range m.fullAdj[node] {
			if id == skipID {
				continue
			}
			*pending = append(*pending, id)
			ed := m.edges[id]
			other := ed.u
			if other == node {
				other = ed.v
			}
			if mark[other] == -nodeColor {
				consider(id)
			}
		}

		for id := range m.forestAdj[node] {
			ed := m.edges[id]
			other := ed.u
			if other == node {
				other = ed.v
			}
			if mark[other] != m.sideStamp && mark[other] != -m.sideStamp {
				mark[other] = nodeColor
				queue = append(queue, other)
			}
		}
		return queue
	}

	side := 1
	for len(queueA) > 0 && len(queueB) > 0 {
		var node int
		var nodeColor int
		if side == 1 {
			node = queueA[0]
			queueA = queueA[1:]
			nodeColor = m.sideStamp
			queueA = scanNode(node, nodeColor, queueA, &pendingA)
		} else {
			node = queueB[0]
			queueB = queueB[1:]
			nodeColor = -m.sideStamp
			queueB = scanNode(node, nodeColor, queueB, &pendingB)
		}
		side = 3 - side
		if len(queueA) == 0 || len(queueB) == 0 {
			break
		}
	}

	var pending []int
	var smallColor int
	if len(queueA) == 0 {
		pending = pendingA
		smallColor = m.sideStamp
	} else {
		pending = pendingB
		smallColor = -m.sideStamp
	}
	for _, candidateID := range pending {
		ed, ok := m.edges[candidateID]
		if !ok || !ed.alive {
			continue
		}
		if mark[ed.u] != smallColor || mark[ed.v] != smallColor {
			consider(candidateID)
		}
	}

	for id := range m.candidateMark {
		delete(m.candidateMark, id)
	}
	return bestID
}
