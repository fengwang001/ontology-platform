package ontology

import (
	"cmp"
	"container/heap"
	"fmt"
	"strings"
)

// Query runs a shortest-path query and hides the internal access metric.
func (s *Store) Query(q Query) (Result, error) {
	res, _, err := s.QueryWithStats(q)
	return res, err
}

// QueryWithStats additionally returns the internal access metric used to
// verify that query cost scales with the explored neighborhood only.
//
// The search is a best-first search over states
// (object, NFA position, depth, visited-set) ordered by the path key
// (total cost, category rank sequence, object ID sequence, link ID
// sequence). Every key component is append-only, so the key never
// decreases along a path extension and the first accepting end state
// popped from the queue is optimal. A plain label-setting search over
// (object, NFA position) would be unsound because of the simple-path
// requirement, so visited sets are part of the state; safe domination
// (same object/position/depth, subset visited set, strictly smaller key)
// prunes provably suboptimal states.
func (s *Store) QueryWithStats(q Query) (Result, QueryStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Parameter validation, always before any graph search.
	cc, err := s.compile(q.Constraint)
	if err != nil {
		return Result{}, QueryStats{}, err
	}
	start, ok := s.objects[q.Start]
	if !ok {
		return Result{}, QueryStats{}, fmt.Errorf("%w: start object %q not found", ErrInvalidParams, q.Start)
	}
	end, ok := s.objects[q.End]
	if !ok {
		return Result{}, QueryStats{}, fmt.Errorf("%w: end object %q not found", ErrInvalidParams, q.End)
	}

	// 2. Forbidden object types, checked before reachability.
	if s.forbidden[start.typ] || s.forbidden[end.typ] {
		return Result{}, QueryStats{}, ErrForbiddenType
	}

	// 3. Degenerate case: start == end admits only the empty path, and
	// only when the constraint matches an empty category sequence.
	if q.Start == q.End {
		if cc.accepting(0) {
			return Result{
				Found: true,
				Path:  Path{Objects: []ObjectID{q.Start}},
			}, QueryStats{}, nil
		}
		return Result{}, QueryStats{}, nil
	}

	// 4. Best-first search.
	sch := &search{
		s:        s,
		q:        q,
		cc:       cc,
		objIdx:   make(map[ObjectID]int),
		dom:      make(map[domKey][]domEntry),
		expanded: make(map[ObjectID]bool),
	}
	return sch.run()
}

// pathState is one search state: a simple path from the start object to
// obj, together with the NFA position reached by its category sequence.
type pathState struct {
	obj     ObjectID
	nfa     int
	cost    uint64
	cats    []int      // category ranks, len == depth
	objs    []ObjectID // len == depth+1
	links   []LinkID   // len == depth
	visited []uint64   // bitmap over per-query object indices
}

// cmpSeq compares two sequences element-wise; a strict prefix is smaller
// than its extension.
func cmpSeq[T cmp.Ordered](a, b []T) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// cmpKey orders by the full path key: cost, category ranks, object IDs,
// link IDs. The link ID sequence is internal only: it makes the result
// deterministic among equivalent paths and is never part of the
// equivalence relation.
func cmpKey(a, b pathState) int {
	if a.cost != b.cost {
		if a.cost < b.cost {
			return -1
		}
		return 1
	}
	if c := cmpSeq(a.cats, b.cats); c != 0 {
		return c
	}
	if c := cmpSeq(a.objs, b.objs); c != 0 {
		return c
	}
	return cmpSeq(a.links, b.links)
}

// cmpEquivKey orders by the equivalence key: cost, category ranks,
// object IDs. Two paths with equal equivalence keys are equivalent.
func cmpEquivKey(a, b pathState) int {
	if a.cost != b.cost {
		if a.cost < b.cost {
			return -1
		}
		return 1
	}
	if c := cmpSeq(a.cats, b.cats); c != 0 {
		return c
	}
	return cmpSeq(a.objs, b.objs)
}

// stateHeap is a priority queue ordered by the full path key.
type stateHeap []pathState

func (h stateHeap) Len() int           { return len(h) }
func (h stateHeap) Less(i, j int) bool { return cmpKey(h[i], h[j]) < 0 }
func (h stateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stateHeap) Push(x any)        { *h = append(*h, x.(pathState)) }
func (h *stateHeap) Pop() any {
	old := *h
	n := len(old)
	st := old[n-1]
	*h = old[:n-1]
	return st
}

// setBit returns a copy of the bitmap with bit i set.
func setBit(v []uint64, i int) []uint64 {
	w := i / 64
	nv := make([]uint64, max(len(v), w+1))
	copy(nv, v)
	nv[w] |= 1 << uint(i%64)
	return nv
}

func hasBit(v []uint64, i int) bool {
	w := i / 64
	return w < len(v) && v[w]&(1<<uint(i%64)) != 0
}

// subsetBits reports whether every set bit of a is also set in b.
// Missing words count as zero.
func subsetBits(a, b []uint64) bool {
	for i, w := range a {
		if i < len(b) {
			if w&^b[i] != 0 {
				return false
			}
		} else if w != 0 {
			return false
		}
	}
	return true
}

// domKey identifies the domination class of a state. Domination is only
// sound within one class: equal object, NFA position and depth guarantee
// that appending a common suffix preserves the key order.
type domKey struct {
	obj   ObjectID
	nfa   int
	depth int
}

type domEntry struct {
	visited []uint64
	cost    uint64
	cats    []int
	objs    []ObjectID
}

// strictLess compares (cost, cats, objs) of same-depth paths.
func (e domEntry) strictLess(cost uint64, cats []int, objs []ObjectID) bool {
	if e.cost != cost {
		return e.cost < cost
	}
	if c := cmpSeq(e.cats, cats); c != 0 {
		return c < 0
	}
	return cmpSeq(e.objs, objs) < 0
}

type search struct {
	s  *Store
	q  Query
	cc *compiledConstraint

	objIdx   map[ObjectID]int // per-query bitmap index assignment
	dom      map[domKey][]domEntry
	expanded map[ObjectID]bool // distinct objects already scanned
	stats    QueryStats
}

func (sch *search) bitOf(id ObjectID) int {
	if i, ok := sch.objIdx[id]; ok {
		return i
	}
	i := len(sch.objIdx)
	sch.objIdx[id] = i
	return i
}

func (sch *search) run() (Result, QueryStats, error) {
	pq := &stateHeap{}
	heap.Init(pq)
	startBit := setBit(nil, sch.bitOf(sch.q.Start))
	for _, p0 := range sch.cc.closure(0) {
		heap.Push(pq, pathState{
			obj:     sch.q.Start,
			nfa:     p0,
			objs:    []ObjectID{sch.q.Start},
			visited: startBit,
		})
	}

	var opt *pathState
	equiv := map[string]bool{}
	for pq.Len() > 0 {
		st := heap.Pop(pq).(pathState)
		if opt != nil {
			// Draining: collect accepting states equivalent to the
			// optimum. Successors of non-accepting states here are
			// strictly worse, so they are discarded unexpanded.
			if cmpEquivKey(st, *opt) != 0 {
				break
			}
			if st.obj == sch.q.End && sch.cc.accepting(st.nfa) {
				equiv[linkKey(st.links)] = true
			}
			continue
		}
		if st.obj == sch.q.End {
			// Never expand through the end object: the path would
			// revisit it, violating the simple-path rule.
			if sch.cc.accepting(st.nfa) {
				cp := st
				opt = &cp
				equiv[linkKey(st.links)] = true
			}
			continue
		}
		sch.expand(pq, st)
	}
	if opt == nil {
		return Result{}, sch.stats, nil
	}

	rankToCat := make([]Category, len(sch.s.catRank))
	for c, r := range sch.s.catRank {
		rankToCat[r] = c
	}
	cats := make([]Category, len(opt.cats))
	for i, r := range opt.cats {
		cats[i] = rankToCat[r]
	}
	return Result{
		Found:      true,
		Equivalent: len(equiv) > 1,
		Path: Path{
			Objects:    opt.objs,
			Links:      opt.links,
			Categories: cats,
			TotalCost:  opt.cost,
		},
	}, sch.stats, nil
}

// expand generates the successors of st. The filter order is fixed:
// visited set, then isolation, then permission visibility, then the
// category transition.
func (sch *search) expand(pq *stateHeap, st pathState) {
	s := sch.s
	if !sch.expanded[st.obj] {
		sch.expanded[st.obj] = true
		sch.stats.ObjectsVisited++
	}
	o := s.objects[st.obj]
	for _, e := range o.adj {
		sch.stats.LinksExamined++
		w := e.to
		wIdx := sch.bitOf(w)
		if hasBit(st.visited, wIdx) {
			continue // simple path: no repeated objects
		}
		// Isolation is always evaluated before permission visibility.
		if w != sch.q.End && s.objects[w].isolated {
			continue // isolated objects may not be intermediate nodes
		}
		l := s.links[e.link]
		lt := s.linkTypes[l.typ]
		if lt.Principals != nil && !lt.Principals[sch.q.Principal] {
			continue // invisible link type: treated as nonexistent
		}
		r := s.catRank[lt.Category]
		for _, np := range sch.cc.next(st.nfa, r) {
			if w == sch.q.End && !sch.cc.accepting(np) {
				continue // a path ending at End must match fully
			}
			ns := pathState{
				obj:     w,
				nfa:     np,
				cost:    st.cost + uint64(lt.Cost),
				cats:    append(append([]int{}, st.cats...), r),
				objs:    append(append([]ObjectID{}, st.objs...), w),
				links:   append(append([]LinkID{}, st.links...), e.link),
				visited: setBit(st.visited, wIdx),
			}
			if sch.dominated(ns) {
				continue
			}
			sch.record(ns)
			heap.Push(pq, ns)
		}
	}
}

// dominated reports whether an already generated state provably beats
// ns: same object, NFA position and depth, a visited set that is a
// subset of ns's, and a strictly smaller key. Any completion of ns is
// then also valid for that state with a strictly smaller key, so ns can
// never lead to an optimal path.
func (sch *search) dominated(ns pathState) bool {
	for _, e := range sch.dom[domKey{obj: ns.obj, nfa: ns.nfa, depth: len(ns.cats)}] {
		if subsetBits(e.visited, ns.visited) && e.strictLess(ns.cost, ns.cats, ns.objs) {
			return true
		}
	}
	return false
}

// record registers ns in the domination index and drops entries that ns
// itself dominates.
func (sch *search) record(ns pathState) {
	k := domKey{obj: ns.obj, nfa: ns.nfa, depth: len(ns.cats)}
	entries := sch.dom[k]
	out := entries[:0]
	for _, e := range entries {
		if subsetBits(ns.visited, e.visited) && ns.strictLessDom(e) {
			continue
		}
		out = append(out, e)
	}
	sch.dom[k] = append(out, domEntry{
		visited: ns.visited,
		cost:    ns.cost,
		cats:    ns.cats,
		objs:    ns.objs,
	})
}

func (ns pathState) strictLessDom(e domEntry) bool {
	if ns.cost != e.cost {
		return ns.cost < e.cost
	}
	if c := cmpSeq(ns.cats, e.cats); c != 0 {
		return c < 0
	}
	return cmpSeq(ns.objs, e.objs) < 0
}

// linkKey identifies a path by its link instance sequence; distinct
// paths always have distinct link keys.
func linkKey(links []LinkID) string {
	parts := make([]string, len(links))
	for i, l := range links {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\x00")
}
