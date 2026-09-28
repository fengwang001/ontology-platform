package reachability

import (
	"sort"
	"sync"
)

// Reachability is a directed multigraph that incrementally maintains the set
// of all reachable ordered node pairs.
//
// A pair (u, v) is reachable when there is a directed walk of at least one
// edge from u to v. Self loops are allowed, so (u, u) is reachable whenever u
// lies on a directed cycle.
//
// All methods are safe for concurrent use. Each method call observes a whole
// state: readers never see a partially applied update.
type Reachability struct {
	mu sync.RWMutex

	// maxEdges bounds the number of distinct edges with positive multiplicity.
	// A non-positive value means unlimited.
	maxEdges int

	// edges holds the multiplicity of each ordered edge.
	edges map[[2]string]int

	// succ is the adjacency view, containing exactly the edges with positive
	// multiplicity.
	succ map[string]map[string]struct{}

	// reach is the maintained transitive closure of succ, excluding the
	// reflexive pairs that are not backed by a real walk.
	reach map[string]map[string]struct{}
}

// New creates an empty graph. maxEdges bounds the number of distinct edges;
// a value <= 0 means unlimited. A negative limit is rejected.
func New(maxEdges int) (*Reachability, error) {
	if maxEdges < 0 {
		return nil, ErrInvalidArgument
	}
	return &Reachability{
		maxEdges: maxEdges,
		edges:    make(map[[2]string]int),
		succ:     make(map[string]map[string]struct{}),
		reach:    make(map[string]map[string]struct{}),
	}, nil
}

// AddEdge adds one occurrence of the edge from -> to, creating it if it does
// not exist yet. Adding an edge that already exists increments its
// multiplicity and leaves the reachability set unchanged.
//
// It returns ErrEmptyNode for an empty endpoint, or ErrTooManyEdges when
// creating the edge would exceed the edge limit. A rejected call changes
// nothing.
func (r *Reachability) AddEdge(from, to string) error {
	if from == "" || to == "" {
		return ErrEmptyNode
	}
	key := [2]string{from, to}

	r.mu.Lock()
	defer r.mu.Unlock()

	m := r.edges[key]
	if m == 0 {
		if r.maxEdges > 0 && len(r.edges) >= r.maxEdges {
			return ErrTooManyEdges
		}
		r.edges[key] = 1
		r.addSuccLocked(from, to)
		r.addEdgeLocked(from, to)
	} else {
		r.edges[key] = m + 1
	}
	return nil
}

// RemoveEdge removes one occurrence of the edge from -> to. When the last
// occurrence is removed, every reachability pair that could use the edge is
// removed first and the pairs still reachable are then re-derived.
//
// It returns ErrEmptyNode for an empty endpoint, or ErrEdgeNotFound when the
// edge has multiplicity zero. A rejected call changes nothing.
func (r *Reachability) RemoveEdge(from, to string) error {
	if from == "" || to == "" {
		return ErrEmptyNode
	}
	key := [2]string{from, to}

	r.mu.Lock()
	defer r.mu.Unlock()

	m := r.edges[key]
	if m == 0 {
		return ErrEdgeNotFound
	}
	if m > 1 {
		r.edges[key] = m - 1
		return nil
	}

	delete(r.edges, key)
	r.removeSuccLocked(from, to)
	r.removeEdgeLocked(from, to)
	return nil
}

// Reachable reports whether a walk of at least one edge exists from "from" to
// "to". It returns ErrEmptyNode for an empty endpoint.
func (r *Reachability) Reachable(from, to string) (bool, error) {
	if from == "" || to == "" {
		return false, ErrEmptyNode
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.reach[from][to]
	return ok, nil
}

// Path returns a shortest witnessing walk as the ordered list of node names,
// including both endpoints. The boolean is false when no such walk exists.
// It returns ErrEmptyNode for an empty endpoint.
func (r *Reachability) Path(from, to string) ([]string, bool, error) {
	if from == "" || to == "" {
		return nil, false, ErrEmptyNode
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p := r.bfsPathLocked(from, to)
	if p == nil {
		return nil, false, nil
	}
	return p, true, nil
}

// Pairs returns all reachable ordered pairs in lexicographic order: sorted by
// source, then by target.
func (r *Reachability) Pairs() [][2]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.pairsLocked()
}

// PairWitness is a reachable pair together with a shortest path witnessing
// it. The path includes both endpoints and has at least one edge.
type PairWitness struct {
	From string
	To   string
	Path []string
}

// Snapshot returns every reachable pair with a shortest witnessing path,
// sorted lexicographically. Everything is gathered under a single read lock,
// so the result is point-in-time and per-pair consistent: a concurrent writer
// can never make one pair reflect state N and another reflect state N+1.
func (r *Reachability) Snapshot() []PairWitness {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pairs := r.pairsLocked()
	out := make([]PairWitness, 0, len(pairs))
	for _, p := range pairs {
		path := r.bfsPathLocked(p[0], p[1])
		out = append(out, PairWitness{From: p[0], To: p[1], Path: path})
	}
	return out
}

// EdgeMultiplicity returns the current multiplicity of the edge from -> to.
// It returns ErrEmptyNode for an empty endpoint.
func (r *Reachability) EdgeMultiplicity(from, to string) (int, error) {
	if from == "" || to == "" {
		return 0, ErrEmptyNode
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.edges[[2]string{from, to}], nil
}

// Nodes returns every node that participates in at least one edge, in sorted
// order. (Every node occurring in a reachable pair participates in an edge.)
func (r *Reachability) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]struct{})
	for u, outs := range r.succ {
		seen[u] = struct{}{}
		for v := range outs {
			seen[v] = struct{}{}
		}
	}
	nodes := make([]string, 0, len(seen))
	for n := range seen {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return nodes
}

// addSuccLocked inserts an edge into the adjacency view.
func (r *Reachability) addSuccLocked(from, to string) {
	outs := r.succ[from]
	if outs == nil {
		outs = make(map[string]struct{})
		r.succ[from] = outs
	}
	outs[to] = struct{}{}
}

// removeSuccLocked deletes an edge from the adjacency view, dropping empty
// adjacency sets.
func (r *Reachability) removeSuccLocked(from, to string) {
	if outs := r.succ[from]; outs != nil {
		delete(outs, to)
		if len(outs) == 0 {
			delete(r.succ, from)
		}
	}
}

// pairsLocked collects and sorts all reachable pairs.
// The caller must hold r.mu for reading.
func (r *Reachability) pairsLocked() [][2]string {
	pairs := make([][2]string, 0)
	for u, outs := range r.reach {
		for v := range outs {
			pairs = append(pairs, [2]string{u, v})
		}
	}
	sortPairs(pairs)
	return pairs
}

// sortPairs orders pairs lexicographically by source then target.
func sortPairs(pairs [][2]string) {
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
}
