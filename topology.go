package ontology

import (
	"sort"
	"sync"
)

type AddEdgeResult struct {
	Moved    []int
	Forward  int
	Backward int
}

type AddEdgesResult struct {
	Moved  []int
	Counts []EdgeCount
}

type EdgeCount struct {
	Forward  int
	Backward int
}

type Maintainer struct {
	mu        sync.RWMutex
	nodeLimit int
	edgeLimit int
	created   int
	edgeCount int
	touched   int
	alive     []bool
	ord       []int
	out       map[int]map[int]struct{}
	in        map[int]map[int]struct{}
}

func NewMaintainer(n, e int) *Maintainer {
	if n < 1 || n > 100000 || e < 1 || e > 500000 {
		panic("ontology: limits out of range")
	}
	return &Maintainer{
		nodeLimit: n,
		edgeLimit: e,
		alive:     make([]bool, n),
		ord:       make([]int, n),
		out:       make(map[int]map[int]struct{}, n),
		in:        make(map[int]map[int]struct{}, n),
	}
}

func (m *Maintainer) AddNode() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.created == m.nodeLimit {
		return 0, ErrNodeLimit
	}
	x := m.created
	m.created++
	m.alive[x] = true
	m.ord[x] = x
	m.out[x] = make(map[int]struct{})
	m.in[x] = make(map[int]struct{})
	return x, nil
}

func (m *Maintainer) RemoveNode(x int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.live(x) {
		return ErrNodeNotFound
	}

	for y := range m.out[x] {
		delete(m.in[y], x)
		m.edgeCount--
	}
	for y := range m.in[x] {
		delete(m.out[y], x)
		m.edgeCount--
	}
	m.out[x] = nil
	m.in[x] = nil
	m.alive[x] = false
	return nil
}

func (m *Maintainer) Order() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	live := make([]int, 0, m.created)
	for x := 0; x < m.created; x++ {
		if m.alive[x] {
			live = append(live, x)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		a, b := live[i], live[j]
		if m.ord[a] != m.ord[b] {
			return m.ord[a] < m.ord[b]
		}
		return a < b
	})
	return live
}

func (m *Maintainer) OrdOf(x int) (int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.live(x) {
		return 0, false
	}
	return m.ord[x], true
}

func (m *Maintainer) Touched() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.touched
}

func (m *Maintainer) live(x int) bool {
	return x >= 0 && x < m.created && m.alive[x]
}
