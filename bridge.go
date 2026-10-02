package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidN       = errors.New("N must be between 1 and 100000")
	ErrInvalidE       = errors.New("E must be between 1 and 500000")
	ErrInvalidNode    = errors.New("node index out of range")
	ErrSelfLoop       = errors.New("undirected self loops are not supported")
	ErrInvalidWeight  = errors.New("weight must be between 1 and 1000000")
	ErrEdgeLimit      = errors.New("live edge limit reached")
	ErrEdgeNotFound   = errors.New("edge id has not been allocated")
	ErrNotConnected   = errors.New("nodes are not connected")
	ErrNoBridgeOnPath = errors.New("path contains no bridge")
)

type AddKind string

const (
	KindLink   AddKind = "Link"
	KindMerge  AddKind = "Merge"
	KindInside AddKind = "Inside"
)

type AddResult struct {
	ID        int
	Version   int
	Kind      AddKind
	Merged    []int
	NewLabel  int
	Unbridged []int
	NewWeight int64
}

type BridgeWeight struct {
	ID     int
	Weight int64
}

type IncrementalBridges struct {
	mu sync.RWMutex

	n        int
	maxEdges int
	edges    []edgeRecord
	version  int
	bridges  int
	blocks   int
	steps    int64

	parent []int
	size   []int

	blockParent []int
	label       []int
	blockWeight []int64

	lctParent []int
	lctChild  [][2]int
	lctRev    []bool
	lctMin    []bridgeValue
	lctFirst  []int
	lctEdgeID []int
	lctWeight []int64

	nextNode int
}

type edgeRecord struct {
	u         int
	v         int
	weight    int64
	born      int
	unbridged int
	bridge    bool
}

type bridgeValue struct {
	weight int64
	id     int
	empty  bool
}

func (m *IncrementalBridges) edgeNode(id int) int {
	return m.n + id + 1
}

func (m *IncrementalBridges) vertexNode(x int) int {
	return x + 1
}

func (m *IncrementalBridges) nextLCTNode() int {
	m.nextNode++
	return m.nextNode
}

func (m *IncrementalBridges) newLCTNode(edgeID int, weight int64) int {
	node := m.nextLCTNode()
	m.lctEdgeID[node] = edgeID
	m.lctWeight[node] = weight
	m.lctMin[node] = bridgeValue{weight: weight, id: edgeID}
	return node
}

func (m *IncrementalBridges) neutralValue() bridgeValue {
	return bridgeValue{empty: true}
}

func NewIncrementalBridges(n, e int) (*IncrementalBridges, error) {
	if n < 1 || n > 100000 {
		return nil, ErrInvalidN
	}
	if e < 1 || e > 500000 {
		return nil, ErrInvalidE
	}
	maxNodes := n + e + (n - 1)
	m := &IncrementalBridges{
		n:           n,
		maxEdges:    e,
		blocks:      n,
		edges:       make([]edgeRecord, 1, e+1),
		parent:      make([]int, n),
		size:        make([]int, n),
		blockParent: make([]int, n),
		label:       make([]int, n),
		blockWeight: make([]int64, n),
		lctParent:   make([]int, maxNodes+2),
		lctChild:    make([][2]int, maxNodes+2),
		lctRev:      make([]bool, maxNodes+2),
		lctMin:      make([]bridgeValue, maxNodes+2),
		lctFirst:    make([]int, maxNodes+2),
		lctEdgeID:   make([]int, maxNodes+2),
		lctWeight:   make([]int64, maxNodes+2),
		nextNode:    n + e + 1,
	}
	neutral := m.neutralValue()
	m.lctMin[0] = neutral
	for i := 0; i < n; i++ {
		m.parent[i] = i
		m.size[i] = 1
		m.blockParent[i] = i
		m.label[i] = i
		m.lctMin[m.vertexNode(i)] = neutral
	}
	for i := n + 1; i < len(m.lctMin); i++ {
		m.lctMin[i] = neutral
	}
	return m, nil
}

func (m *IncrementalBridges) AddEdge(u, v int, w int64) (AddResult, error) {
	if u < 0 || u >= m.n || v < 0 || v >= m.n {
		return AddResult{}, ErrInvalidNode
	}
	if u == v {
		return AddResult{}, ErrSelfLoop
	}
	if w < 1 || w > 1_000_000 {
		return AddResult{}, ErrInvalidWeight
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.edges)-1 >= m.maxEdges {
		return AddResult{}, ErrEdgeLimit
	}

	id := len(m.edges)
	m.version++
	rec := edgeRecord{u: u, v: v, weight: w, born: m.version}

	ru := m.findComponent(u)
	rv := m.findComponent(v)
	if ru != rv {
		rec.bridge = true
		m.edges = append(m.edges, rec)
		m.linkBridge(u, v, id, w)
		m.linkComponent(ru, rv)
		m.bridges++
		return AddResult{
			ID:      id,
			Version: m.version,
			Kind:    KindLink,
		}, nil
	}

	bu := m.findBlock(u)
	bv := m.findBlock(v)
	if bu == bv {
		rec.unbridged = m.version
		m.edges = append(m.edges, rec)
		m.blockWeight[bu] += w
		return AddResult{
			ID:      id,
			Version: m.version,
			Kind:    KindInside,
		}, nil
	}

	rec.unbridged = m.version
	m.edges = append(m.edges, rec)
	labels, pathEdges := m.mergeBlocks(u, v)
	newLabel := m.label[m.findBlock(u)]

	totalWeight := int64(0)
	for _, label := range labels {
		totalWeight += m.blockWeight[label]
	}
	sort.Ints(pathEdges)
	unbridgedIDs := make([]int, 0, len(pathEdges))
	for _, edgeID := range pathEdges {
		unbridgedIDs = append(unbridgedIDs, edgeID)
		totalWeight += m.edges[edgeID].weight
		m.edges[edgeID].bridge = false
		m.edges[edgeID].unbridged = m.version
	}
	totalWeight += w

	root := m.findBlock(u)
	m.blockWeight[root] = totalWeight
	m.bridges -= len(pathEdges)
	m.blocks -= len(labels) - 1

	return AddResult{
		ID:        id,
		Version:   m.version,
		Kind:      KindMerge,
		Merged:    labels,
		NewLabel:  newLabel,
		Unbridged: unbridgedIDs,
		NewWeight: totalWeight,
	}, nil
}

func (m *IncrementalBridges) findComponent(x int) int {
	for m.parent[x] != x {
		m.parent[x] = m.parent[m.parent[x]]
		x = m.parent[x]
	}
	return x
}

func (m *IncrementalBridges) linkComponent(a, b int) {
	if m.size[a] < m.size[b] {
		a, b = b, a
	}
	m.parent[b] = a
	m.size[a] += m.size[b]
}

func (m *IncrementalBridges) findBlock(x int) int {
	for m.blockParent[x] != x {
		m.blockParent[x] = m.blockParent[m.blockParent[x]]
		x = m.blockParent[x]
	}
	return x
}
