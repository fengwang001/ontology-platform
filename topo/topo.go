// Package topo implements an incremental topological order maintainer for a
// directed acyclic graph. It keeps a deterministic topological order across
// node/edge insertions and deletions, reordering only the affected interval,
// and rejects cycle-creating edges with a deterministic cycle witness.
package topo

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Distinguishable rejection reasons.
var (
	ErrNodeNotFound = errors.New("topo: node does not exist")
	ErrEdgeExists   = errors.New("topo: edge already exists")
	ErrEdgeLimit    = errors.New("topo: live edge count limit reached")
	ErrNodeLimit    = errors.New("topo: node creation limit reached")
	ErrEdgeNotFound = errors.New("topo: edge does not exist")
	ErrBatchSize    = errors.New("topo: batch size out of range [1,1000]")
)

// CycleError reports that an edge addition would create a cycle. Path is the
// witness: the path from v to u with the fewest edges (ties broken by the
// lexicographically smallest node-id sequence), written [v ... u]; appending
// the rejected edge u->v closes the cycle. For u == v, Path is [u].
type CycleError struct {
	Path []int
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("topo: edge would create a cycle, witness path %v", e.Path)
}

// BatchError reports the failure of one entry inside AddEdges. Index is the
// 0-based position of the rejected entry; Reason is its distinguishable cause
// (a *CycleError when the entry would create a cycle). The whole batch is
// rolled back atomically when a BatchError is returned.
type BatchError struct {
	Index  int
	Reason error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("topo: batch entry %d rejected: %v", e.Index, e.Reason)
}

func (e *BatchError) Unwrap() error { return e.Reason }

// EdgeResult is the outcome of a successful AddEdge. Moved lists the nodes
// whose order value changed, sorted by new order value ascending. DeltaF and
// DeltaB are |deltaF| and |deltaB| of the reordering (0,0 when no reordering
// was needed).
type EdgeResult struct {
	Moved  []int
	DeltaF int
	DeltaB int
}

// BatchResult is the outcome of a successful AddEdges batch. Moved lists the
// nodes whose order value differs between before and after the whole batch,
// sorted by new order value ascending. Counts[i] holds (|deltaF|, |deltaB|)
// of the i-th entry, (0,0) for entries that needed no reordering.
type BatchResult struct {
	Moved  []int
	Counts [][2]int
}

// Maintainer keeps a deterministic topological order of a DAG. All methods
// are safe for concurrent use; the result is equivalent to some serial order.
type Maintainer struct {
	mu        sync.Mutex
	maxNodes  int
	maxEdges  int
	nextID    int // total nodes ever created; ids are never reused
	edgeCount int
	alive     map[int]bool
	ord       map[int]int
	out       map[int]map[int]bool
	in        map[int]map[int]bool
	touched   int64 // nodes marked by AddEdge/AddEdges reordering attempts
}

// New creates a Maintainer allowing at most n node creations (1..100000) and
// at most e live edges (1..500000). It panics on out-of-range limits.
func New(n, e int) *Maintainer {
	if n < 1 || n > 100000 {
		panic(fmt.Sprintf("topo: node limit %d out of range [1,100000]", n))
	}
	if e < 1 || e > 500000 {
		panic(fmt.Sprintf("topo: edge limit %d out of range [1,500000]", e))
	}
	return &Maintainer{
		maxNodes: n,
		maxEdges: e,
		alive:    make(map[int]bool),
		ord:      make(map[int]int),
		out:      make(map[int]map[int]bool),
		in:       make(map[int]map[int]bool),
	}
}

// AddNode creates a node. Ids start at 0 and increase strictly; deleted ids
// are never reused. The new node's order value equals its id.
func (m *Maintainer) AddNode() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nextID >= m.maxNodes {
		return 0, ErrNodeLimit
	}
	id := m.nextID
	m.nextID++
	m.alive[id] = true
	m.ord[id] = id
	m.out[id] = make(map[int]bool)
	m.in[id] = make(map[int]bool)
	return id, nil
}

// AddEdge adds the edge u->v.
func (m *Maintainer) AddEdge(u, v int) (EdgeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addEdge(u, v, nil)
}

// AddEdges applies the entries in order; each entry follows all AddEdge
// rules and sees the edges added by earlier entries. On the first rejected
// entry the whole batch is rolled back atomically (order values, edge set,
// creation counter and touched are restored) and a *BatchError carries the
// entry index and its reason. On success it returns the batch-level Moved
// (nodes whose order value differs from before the batch, by new order
// value ascending) and the per-entry (|deltaF|, |deltaB|) counts.
func (m *Maintainer) AddEdges(edges [][2]int) (BatchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(edges) < 1 || len(edges) > 1000 {
		return BatchResult{}, ErrBatchSize
	}
	before := make(map[int]int, len(m.ord))
	for x, o := range m.ord {
		before[x] = o
	}
	savedTouched := m.touched
	undo := &undoLog{}
	res := BatchResult{Counts: make([][2]int, len(edges))}
	for i, e := range edges {
		r, err := m.addEdge(e[0], e[1], undo)
		if err != nil {
			m.rollback(undo, savedTouched)
			return BatchResult{}, &BatchError{Index: i, Reason: err}
		}
		res.Counts[i] = [2]int{r.DeltaF, r.DeltaB}
	}
	for x, o := range m.ord {
		if before[x] != o {
			res.Moved = append(res.Moved, x)
		}
	}
	sort.Slice(res.Moved, func(i, j int) bool { return m.ord[res.Moved[i]] < m.ord[res.Moved[j]] })
	return res, nil
}

// RemoveEdge removes the edge u->v without changing any order value.
func (m *Maintainer) RemoveEdge(u, v int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.alive[u] || !m.alive[v] {
		return ErrNodeNotFound
	}
	if !m.out[u][v] {
		return ErrEdgeNotFound
	}
	m.removeEdge(u, v)
	return nil
}

// RemoveNode removes a node and all its incident edges. Other order values
// are unchanged and the removed order value is never reassigned.
func (m *Maintainer) RemoveNode(x int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.alive[x] {
		return ErrNodeNotFound
	}
	for w := range m.out[x] {
		delete(m.in[w], x)
		m.edgeCount--
	}
	for w := range m.in[x] {
		delete(m.out[w], x)
		m.edgeCount--
	}
	delete(m.alive, x)
	delete(m.ord, x)
	delete(m.out, x)
	delete(m.in, x)
	return nil
}

// Order returns the live node ids sorted by order value ascending.
func (m *Maintainer) Order() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.orderLocked()
}

// OrdOf returns the order value of a live node.
func (m *Maintainer) OrdOf(x int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.alive[x] {
		return 0, ErrNodeNotFound
	}
	return m.ord[x], nil
}

// Touched returns the cumulative number of nodes marked by reordering
// attempts. It proves reorderings are local, never whole-graph re-sorts.
func (m *Maintainer) Touched() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.touched
}

func (m *Maintainer) addEdge(u, v int, undo *undoLog) (EdgeResult, error) {
	if !m.alive[u] || !m.alive[v] {
		return EdgeResult{}, ErrNodeNotFound
	}
	if u == v {
		return EdgeResult{}, &CycleError{Path: []int{u}}
	}
	if m.out[u][v] {
		return EdgeResult{}, ErrEdgeExists
	}
	if m.edgeCount >= m.maxEdges {
		return EdgeResult{}, ErrEdgeLimit
	}
	res := EdgeResult{}
	if m.ord[u] < m.ord[v] {
		m.insertEdge(u, v, undo)
		return res, nil
	}
	lb, ub := m.ord[v], m.ord[u]
	// deltaF: nodes reachable from v along out-edges with ord <= ub.
	deltaF := m.scanForward(v, ub)
	if _, hit := deltaF[u]; hit {
		// Rejected: touched must not change. The scan only marked nodes
		// inside [lb,ub], never the whole graph.
		return EdgeResult{}, &CycleError{Path: m.witness(v, u)}
	}
	// deltaB: nodes that reach u along in-edges with ord >= lb.
	deltaB := m.scanBackward(u, lb)
	m.touched += int64(len(deltaF) + len(deltaB))

	// Pool: old order values of deltaB and deltaF, ascending. deltaB sorted
	// by old ord comes first, then deltaF sorted by old ord; the i-th node
	// receives the i-th pool value.
	seqB := sortedByOrd(deltaB, m.ord)
	seqF := sortedByOrd(deltaF, m.ord)
	pool := make([]int, 0, len(seqB)+len(seqF))
	for _, x := range seqB {
		pool = append(pool, m.ord[x])
	}
	for _, x := range seqF {
		pool = append(pool, m.ord[x])
	}
	sort.Ints(pool)
	seq := append(seqB, seqF...)
	moved := make([]int, 0, len(seq))
	for i, x := range seq {
		if m.ord[x] != pool[i] {
			if undo != nil {
				undo.recordOrd(x, m.ord[x])
			}
			m.ord[x] = pool[i]
			moved = append(moved, x)
		}
	}
	sort.Slice(moved, func(i, j int) bool { return m.ord[moved[i]] < m.ord[moved[j]] })
	res.Moved = moved
	res.DeltaF = len(deltaF)
	res.DeltaB = len(deltaB)
	m.insertEdge(u, v, undo)
	return res, nil
}

func (m *Maintainer) insertEdge(u, v int, undo *undoLog) {
	m.out[u][v] = true
	m.in[v][u] = true
	m.edgeCount++
	if undo != nil {
		undo.edges = append(undo.edges, [2]int{u, v})
	}
}

// scanForward returns the set of nodes reachable from v along out-edges
// whose order value is at most ub (v included).
func (m *Maintainer) scanForward(v, ub int) map[int]struct{} {
	seen := map[int]struct{}{v: {}}
	queue := []int{v}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for w := range m.out[x] {
			if _, ok := seen[w]; !ok && m.ord[w] <= ub {
				seen[w] = struct{}{}
				queue = append(queue, w)
			}
		}
	}
	return seen
}

// scanBackward returns the set of nodes that reach u along in-edges
// whose order value is at least lb (u included).
func (m *Maintainer) scanBackward(u, lb int) map[int]struct{} {
	seen := map[int]struct{}{u: {}}
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for w := range m.in[x] {
			if _, ok := seen[w]; !ok && m.ord[w] >= lb {
				seen[w] = struct{}{}
				queue = append(queue, w)
			}
		}
	}
	return seen
}

func sortedByOrd(set map[int]struct{}, ord map[int]int) []int {
	nodes := make([]int, 0, len(set))
	for x := range set {
		nodes = append(nodes, x)
	}
	sort.Slice(nodes, func(i, j int) bool { return ord[nodes[i]] < ord[nodes[j]] })
	return nodes
}

// witness returns the deterministic cycle witness: the path from v to u with
// the fewest edges; among equally short paths the lexicographically smallest
// node-id sequence.
func (m *Maintainer) witness(v, u int) []int {
	// dist[x] = fewest edges from x to u, via reverse BFS from u.
	dist := map[int]int{u: 0}
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for w := range m.in[x] {
			if _, ok := dist[w]; !ok {
				dist[w] = dist[x] + 1
				queue = append(queue, w)
			}
		}
	}
	d := dist[v]
	path := make([]int, 0, d+1)
	cur := v
	path = append(path, cur)
	for step := 1; step <= d; step++ {
		next := -1
		for w := range m.out[cur] {
			if dw, ok := dist[w]; ok && dw == d-step {
				if next == -1 || w < next {
					next = w
				}
			}
		}
		cur = next
		path = append(path, cur)
	}
	return path
}

func (m *Maintainer) removeEdge(u, v int) {
	delete(m.out[u], v)
	delete(m.in[v], u)
	m.edgeCount--
}

func (m *Maintainer) orderLocked() []int {
	nodes := make([]int, 0, len(m.alive))
	for x := range m.alive {
		nodes = append(nodes, x)
	}
	sort.Slice(nodes, func(i, j int) bool { return m.ord[nodes[i]] < m.ord[nodes[j]] })
	return nodes
}

func (m *Maintainer) rollback(undo *undoLog, savedTouched int64) {
	for i := len(undo.edges) - 1; i >= 0; i-- {
		m.removeEdge(undo.edges[i][0], undo.edges[i][1])
	}
	for x, o := range undo.oldOrds {
		m.ord[x] = o
	}
	m.touched = savedTouched
}

// undoLog records the changes of one batch so a rejected batch can be
// rolled back atomically.
type undoLog struct {
	edges   [][2]int    // edges inserted by the batch, in order
	oldOrds map[int]int // node -> order value before its first change
}

func (l *undoLog) recordOrd(x, old int) {
	if l.oldOrds == nil {
		l.oldOrds = map[int]int{x: old}
		return
	}
	if _, ok := l.oldOrds[x]; !ok {
		l.oldOrds[x] = old
	}
}
