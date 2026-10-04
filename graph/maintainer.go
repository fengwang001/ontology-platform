// Package graph implements an incremental (insertion-only) maintainer of
// connected components, bridges and 2-edge-connected blocks on an undirected
// multigraph, together with per-edge version history.
//
// Definitions:
//   - A bridge is an edge whose removal splits its connected component into
//     two. Two parallel edges are never bridges.
//   - A 2-edge-connected block is a maximal set of nodes connected through
//     non-bridge edges; an isolated node forms its own block. The label of a
//     block is the smallest node id it contains.
//   - Contracting every block to a single node yields a forest (the bridge
//     tree), hence BlockCount == BridgeCount + ComponentCount at all times.
//
// AddEdge reports one of three kinds:
//   - Link: the endpoints were in different components; the new edge is a
//     bridge and no block changes.
//   - Merge: the endpoints were in the same component but in different
//     blocks; every bridge on the bridge-tree path between the two blocks
//     becomes a non-bridge and every block on that path merges into one.
//     The new edge is a non-bridge.
//   - Inside: the endpoints were already in the same block; nothing changes
//     and the new edge is a non-bridge.
//
// The block weight is the sum of the weights of all non-bridge edges inside
// the block. A bridge's weight belongs to no block; when a bridge becomes a
// non-bridge its weight joins the merged block.
//
// All methods are safe for concurrent use; the outcome is equivalent to some
// serial execution of the calls.
package graph

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Kind classifies the effect of an accepted AddEdge call.
type Kind int

const (
	// KindLink connects two previously disconnected components; the new
	// edge is a bridge.
	KindLink Kind = iota
	// KindMerge closes a cycle across blocks; the bridges on the
	// bridge-tree path become non-bridges and the blocks on the path
	// merge into one.
	KindMerge
	// KindInside adds an edge inside a single block; nothing else changes.
	KindInside
)

// String returns the report name of the kind: Link, Merge or Inside.
func (k Kind) String() string {
	switch k {
	case KindLink:
		return "Link"
	case KindMerge:
		return "Merge"
	case KindInside:
		return "Inside"
	}
	return "Unknown"
}

// Distinguishable failure reasons returned by this package. Use
// errors.Is to test for them.
var (
	// ErrInvalidArgument reports out-of-range constructor arguments,
	// node ids, equal endpoints or out-of-range weights.
	ErrInvalidArgument = errors.New("graph: invalid argument")
	// ErrEdgeLimit reports that the live-edge count already equals E.
	ErrEdgeLimit = errors.New("graph: edge limit reached")
	// ErrNoSuchEdge reports an edge id that was never assigned.
	ErrNoSuchEdge = errors.New("graph: edge does not exist")
	// ErrNotConnected reports path queries whose endpoints lie in
	// different connected components.
	ErrNotConnected = errors.New("graph: nodes are not connected")
	// ErrNoBridge reports a MinBridge query whose path carries no
	// bridge at all.
	ErrNoBridge = errors.New("graph: no bridge on path")
)

const (
	maxNodes  = 100000
	maxEdges  = 500000
	maxWeight = 1000000
)

// AddResult describes the effect of one accepted AddEdge call.
type AddResult struct {
	ID      int  // assigned edge id (strictly increasing from 1)
	Version int  // graph version after this insertion
	Kind    Kind // Link, Merge or Inside
	// Merged lists the old labels of the merged blocks in ascending
	// order; only non-empty for KindMerge.
	Merged []int
	// NewLabel is the label of the merged block (KindMerge), the label
	// of the unchanged block (KindInside) or -1 (KindLink).
	NewLabel int
	// Unbridged lists the ids of pre-existing edges that stopped being
	// bridges, in ascending order; it never contains the new edge.
	Unbridged []int
	// NewWeight is the block weight of the merged block for KindMerge
	// and 0 for the other kinds.
	NewWeight int64
}

// Maintainer incrementally tracks components, bridges and
// 2-edge-connected blocks of an undirected multigraph that only grows.
//
// The zero value is not usable; construct with New. All methods are
// safe for concurrent use.
type Maintainer struct {
	mu sync.Mutex

	n int // node count
	e int // live-edge limit

	version   int // accepted insertions so far
	edgeCount int // assigned edge ids so far

	// Edge table, indexed by edge id (1-based).
	eu       []int
	ev       []int
	ew       []int64
	addedVer []int // version at which the edge was inserted
	freeVer  []int // version at which the edge first became a non-bridge; 0 while still a bridge

	// DSU over 2-edge-connected blocks.
	p2     []int
	label  []int   // smallest node id of the block (valid at roots)
	weight []int64 // block weight (valid at roots)

	// DSU over connected components.
	pcc  []int
	szcc []int

	// Bridge tree over block roots. par[x] may reference a since
	// merged root and is resolved through find2. parEdge[x] is the id
	// of the bridge connecting block x to its parent.
	par     []int
	parEdge []int

	bridges int
	blocks  int
	comps   int

	// Scratch marks for the alternating upward walk.
	lastVisit []int
	iteration int

	// steps accumulates the node hops walked along the bridge tree by
	// AddEdge; it proves merged paths are paid for once.
	steps int64
}

// New creates a maintainer for n nodes (ids 0..n-1) accepting at most e
// live edges. n must be in [1, 100000] and e in [1, 500000].
func New(n, e int) (*Maintainer, error) {
	if n < 1 || n > maxNodes || e < 1 || e > maxEdges {
		return nil, fmt.Errorf("%w: New(n=%d, e=%d), want 1<=n<=%d and 1<=e<=%d",
			ErrInvalidArgument, n, e, maxNodes, maxEdges)
	}
	m := &Maintainer{
		n:         n,
		e:         e,
		eu:        make([]int, e+1),
		ev:        make([]int, e+1),
		ew:        make([]int64, e+1),
		addedVer:  make([]int, e+1),
		freeVer:   make([]int, e+1),
		p2:        make([]int, n),
		label:     make([]int, n),
		weight:    make([]int64, n),
		pcc:       make([]int, n),
		szcc:      make([]int, n),
		par:       make([]int, n),
		parEdge:   make([]int, n),
		blocks:    n,
		comps:     n,
		lastVisit: make([]int, n),
	}
	for i := 0; i < n; i++ {
		m.p2[i] = i
		m.label[i] = i
		m.pcc[i] = i
		m.szcc[i] = 1
		m.par[i] = -1
	}
	return m, nil
}

// find2 returns the root of x's 2-edge-connected block DSU with path
// compression.
func (m *Maintainer) find2(x int) int {
	root := x
	for m.p2[root] != root {
		root = m.p2[root]
	}
	for m.p2[x] != root {
		m.p2[x], x = root, m.p2[x]
	}
	return root
}

// findcc returns the root of x's connected-component DSU with path
// compression.
func (m *Maintainer) findcc(x int) int {
	root := x
	for m.pcc[root] != root {
		root = m.pcc[root]
	}
	for m.pcc[x] != root {
		m.pcc[x], x = root, m.pcc[x]
	}
	return root
}

// makeRoot re-roots the bridge tree of v's component at v by flipping
// the parent pointers on the path from v up to the current root. v must
// be a block root. Every flipped node ends up in a component at least
// twice as large after the surrounding Link, so each node is flipped at
// most ceil(log2 n) times in total.
func (m *Maintainer) makeRoot(v int) {
	child := -1
	childEdge := 0
	for v != -1 {
		m.steps++
		next := -1
		nextEdge := 0
		if p := m.par[v]; p != -1 {
			next = m.find2(p)
			nextEdge = m.parEdge[v]
		}
		m.par[v] = child
		m.parEdge[v] = childEdge
		child = v
		childEdge = nextEdge
		v = next
	}
}

// walkPaths walks the bridge tree upward from the distinct block roots
// a and b (which must lie in the same tree) until the two upward paths
// meet. It returns the lowest common ancestor block root and the two
// paths, each starting at a (resp. b) and ending at the ancestor. When
// countSteps is set the walked hops are charged to m.steps; the charge
// is at most 2*pathLen+4 per call.
func (m *Maintainer) walkPaths(a, b int, countSteps bool) (lca int, pathA, pathB []int) {
	m.iteration++
	iter := m.iteration
	ca, cb := a, b
	for {
		if ca == -1 && cb == -1 {
			break // unreachable for a,b in one tree; safety net
		}
		if ca != -1 {
			if m.lastVisit[ca] == iter {
				lca = ca
				break
			}
			m.lastVisit[ca] = iter
			pathA = append(pathA, ca)
			if countSteps {
				m.steps++
			}
			if p := m.par[ca]; p != -1 {
				ca = m.find2(p)
			} else {
				ca = -1
			}
		}
		if cb != -1 {
			if m.lastVisit[cb] == iter {
				lca = cb
				break
			}
			m.lastVisit[cb] = iter
			pathB = append(pathB, cb)
			if countSteps {
				m.steps++
			}
			if p := m.par[cb]; p != -1 {
				cb = m.find2(p)
			} else {
				cb = -1
			}
		}
	}
	// A pointer may overshoot above the ancestor or stop below it when
	// the other side stalls at the tree root: cut each recorded path
	// right after the ancestor, or extend it upward until the ancestor.
	finish := func(path []int) []int {
		for i, x := range path {
			if x == lca {
				return path[:i+1]
			}
		}
		cur := path[len(path)-1]
		for cur != lca {
			p := m.par[cur]
			if p == -1 {
				break
			}
			cur = m.find2(p)
			path = append(path, cur)
			if countSteps {
				m.steps++
			}
		}
		return path
	}
	pathA = finish(pathA)
	pathB = finish(pathB)
	return lca, pathA, pathB
}

// merge unites every block on the bridge-tree path between the block
// roots a and b into a single block rooted at their lowest common
// ancestor. The bridges on the path become non-bridges at version ver.
// w is the weight of the new edge that triggered the merge.
func (m *Maintainer) merge(a, b, ver int, w int64) AddResult {
	lca, pathA, pathB := m.walkPaths(a, b, true)

	merged := make([]int, 0, len(pathA)+len(pathB)-1)
	unbridged := make([]int, 0, len(pathA)+len(pathB)-2)
	newWeight := w
	newLabel := m.n
	absorb := func(x int) {
		newWeight += m.weight[x]
		if m.label[x] < newLabel {
			newLabel = m.label[x]
		}
		merged = append(merged, m.label[x])
		if x == lca {
			return
		}
		eid := m.parEdge[x]
		unbridged = append(unbridged, eid)
		m.freeVer[eid] = ver
		newWeight += m.ew[eid]
		m.p2[x] = lca
		m.par[x] = lca
		m.parEdge[x] = 0
	}
	for _, x := range pathA {
		absorb(x)
	}
	for _, x := range pathB {
		if x != lca {
			absorb(x)
		}
	}
	m.weight[lca] = newWeight
	m.label[lca] = newLabel

	removed := len(pathA) + len(pathB) - 2 // bridges (and blocks) on the path
	m.bridges -= removed
	m.blocks -= removed

	sort.Ints(merged)
	sort.Ints(unbridged)
	return AddResult{
		Kind:      KindMerge,
		Merged:    merged,
		NewLabel:  newLabel,
		Unbridged: unbridged,
		NewWeight: newWeight,
	}
}

// AddEdge inserts the undirected edge u-v with weight w and reports its
// effect. u and v must be distinct valid node ids and w must be in
// [1, 10^6]. Edge ids are assigned strictly increasingly from 1 and
// rejected insertions consume neither an id nor a version.
//
// Validation order: invalid arguments first, then the edge limit.
func (m *Maintainer) AddEdge(u, v, w int) (AddResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u < 0 || u >= m.n || v < 0 || v >= m.n || u == v || w < 1 || w > maxWeight {
		return AddResult{}, fmt.Errorf("%w: AddEdge(u=%d, v=%d, w=%d) with n=%d",
			ErrInvalidArgument, u, v, w, m.n)
	}
	if m.edgeCount >= m.e {
		return AddResult{}, fmt.Errorf("%w: already holding %d edges", ErrEdgeLimit, m.e)
	}

	id := m.edgeCount + 1
	m.edgeCount++
	m.version++
	ver := m.version
	m.eu[id] = u
	m.ev[id] = v
	m.ew[id] = int64(w)
	m.addedVer[id] = ver

	res := AddResult{ID: id, Version: ver, NewLabel: -1}
	ru, rv := m.find2(u), m.find2(v)
	cu, cv := m.findcc(u), m.findcc(v)

	switch {
	case cu != cv:
		// Link: attach the smaller component's tree (re-rooted at
		// its endpoint's block) under the other endpoint's block.
		if m.szcc[cu] > m.szcc[cv] {
			ru, rv = rv, ru
			cu, cv = cv, cu
		}
		m.makeRoot(ru)
		m.par[ru] = rv
		m.parEdge[ru] = id
		m.pcc[cu] = cv
		m.szcc[cv] += m.szcc[cu]
		m.bridges++
		m.comps--
		res.Kind = KindLink
	case ru == rv:
		// Inside: the new edge is a non-bridge from birth and only
		// grows the block weight.
		m.weight[ru] += int64(w)
		m.freeVer[id] = ver
		res.Kind = KindInside
		res.NewLabel = m.label[ru]
	default:
		// Merge: collapse the bridge-tree path between the blocks.
		res = m.merge(ru, rv, ver, int64(w))
		res.ID = id
		res.Version = ver
		m.freeVer[id] = ver
	}
	return res, nil
}

// IsBridge reports whether edge id is currently a bridge.
func (m *Maintainer) IsBridge(id int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || id > m.edgeCount {
		return false, fmt.Errorf("%w: id=%d, %d edges inserted", ErrNoSuchEdge, id, m.edgeCount)
	}
	return m.freeVer[id] == 0, nil
}

// Block returns the label (smallest node id) of the block holding x.
func (m *Maintainer) Block(x int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNode(x); err != nil {
		return 0, err
	}
	return m.label[m.find2(x)], nil
}

// Connected reports whether u and v lie in the same component.
func (m *Maintainer) Connected(u, v int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNode(u); err != nil {
		return false, err
	}
	if err := m.checkNode(v); err != nil {
		return false, err
	}
	return m.findcc(u) == m.findcc(v), nil
}

// BridgeCount returns the current number of bridges.
func (m *Maintainer) BridgeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bridges
}

// BlockCount returns the current number of 2-edge-connected blocks.
func (m *Maintainer) BlockCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blocks
}

// ComponentCount returns the current number of connected components.
func (m *Maintainer) ComponentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.comps
}

// Version returns the current graph version (accepted insertions).
func (m *Maintainer) Version() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

// EdgeCount returns the number of assigned edge ids.
func (m *Maintainer) EdgeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.edgeCount
}

// BridgesOnPath returns the ids, in ascending order, of every bridge
// that any u-v path must cross. It fails with ErrNotConnected when u
// and v lie in different components.
func (m *Maintainer) BridgesOnPath(u, v int) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNode(u); err != nil {
		return nil, err
	}
	if err := m.checkNode(v); err != nil {
		return nil, err
	}
	if m.findcc(u) != m.findcc(v) {
		return nil, fmt.Errorf("%w: %d and %d", ErrNotConnected, u, v)
	}
	ru, rv := m.find2(u), m.find2(v)
	if ru == rv {
		return []int{}, nil
	}
	lca, pathA, pathB := m.walkPaths(ru, rv, false)
	ids := make([]int, 0, len(pathA)+len(pathB)-2)
	for _, x := range pathA {
		if x != lca {
			ids = append(ids, m.parEdge[x])
		}
	}
	for _, x := range pathB {
		if x != lca {
			ids = append(ids, m.parEdge[x])
		}
	}
	sort.Ints(ids)
	return ids, nil
}

// MinBridge returns the id and weight of the lightest bridge that any
// u-v path must cross; ties on weight are broken by the smaller id. It
// fails with ErrNotConnected for disconnected endpoints and with
// ErrNoBridge when the path carries no bridge.
func (m *Maintainer) MinBridge(u, v int) (int, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNode(u); err != nil {
		return 0, 0, err
	}
	if err := m.checkNode(v); err != nil {
		return 0, 0, err
	}
	if m.findcc(u) != m.findcc(v) {
		return 0, 0, fmt.Errorf("%w: %d and %d", ErrNotConnected, u, v)
	}
	ru, rv := m.find2(u), m.find2(v)
	if ru == rv {
		return 0, 0, fmt.Errorf("%w: %d and %d share a block", ErrNoBridge, u, v)
	}
	lca, pathA, pathB := m.walkPaths(ru, rv, false)
	best := -1
	consider := func(x int) {
		if x == lca {
			return
		}
		eid := m.parEdge[x]
		if best == -1 || m.ew[eid] < m.ew[best] ||
			(m.ew[eid] == m.ew[best] && eid < best) {
			best = eid
		}
	}
	for _, x := range pathA {
		consider(x)
	}
	for _, x := range pathB {
		consider(x)
	}
	return best, m.ew[best], nil
}

// BlockWeight returns the weight of the block holding x: the sum of the
// weights of all non-bridge edges inside that block.
func (m *Maintainer) BlockWeight(x int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNode(x); err != nil {
		return 0, err
	}
	return m.weight[m.find2(x)], nil
}

// EdgeHistory returns the version at which edge id was inserted and the
// version at which it first became a non-bridge (0 if it still is a
// bridge). An edge born as a non-bridge reports equal versions.
func (m *Maintainer) EdgeHistory(id int) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || id > m.edgeCount {
		return 0, 0, fmt.Errorf("%w: id=%d, %d edges inserted", ErrNoSuchEdge, id, m.edgeCount)
	}
	return m.addedVer[id], m.freeVer[id], nil
}

func (m *Maintainer) checkNode(x int) error {
	if x < 0 || x >= m.n {
		return fmt.Errorf("%w: node %d out of range [0,%d)", ErrInvalidArgument, x, m.n)
	}
	return nil
}
