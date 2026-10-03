package roadnet

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
)

// Leg is one traversed edge of a route: the edge id, the chosen
// departure time and the arrival time at the edge's head.
type Leg struct {
	EdgeID int
	Depart int64
	Arrive int64
}

// Result is the answer of an EarliestArrival query.
type Result struct {
	// Arrival is the earliest arrival time at the goal, equal to the
	// last leg's Arrive (or t0 when source == goal).
	Arrival int64
	// Legs is the chosen route: among tight-edge paths it has the
	// fewest edges, then the lexicographically smallest edge id
	// sequence. Each leg departs at the smallest time >= the
	// earliest arrival at its tail that still achieves the leg's
	// arrival time.
	Legs []Leg
	// Popped counts the nodes settled (popped from the priority
	// queue) before the query stopped at the goal.
	Popped int
	// Version is the network version the query was evaluated at.
	Version int64
}

// visibleRecords filters the edge's records down to those visible at
// version ver. Slice order is preserved, so effective times stay
// non-decreasing.
func visibleRecords(e *edge, ver int64) []record {
	recs := make([]record, 0, len(e.records))
	for i := range e.records {
		if e.records[i].visibleAt(ver) {
			recs = append(recs, e.records[i])
		}
	}
	return recs
}

// evalEdge computes arr_e(t): the earliest arrival at the edge's head
// when reaching its tail at time t, waiting arbitrarily and departing
// at any integer time t' >= t outside closed segments. Inside an open
// segment t'+cost grows with t', so only t itself and later segment
// starts can be optimal. It returns the arrival time and the smallest
// departure time achieving it; ok is false when every reachable
// segment is closed.
func evalEdge(recs []record, t int64) (arr, dep int64, ok bool) {
	// Last record with eff <= t is active at t.
	ri0 := sort.Search(len(recs), func(i int) bool { return recs[i].eff > t }) - 1
	if ri0 < 0 {
		return 0, 0, false
	}
	bestArr := int64(math.MaxInt64)
	bestDep := int64(0)
	// Candidate departures: t itself (when the segment holding t is
	// open) and every later open segment start, in increasing order;
	// strict improvement keeps the smallest achieving departure.
	r0 := &recs[ri0]
	j := sort.Search(len(r0.profile), func(j int) bool {
		return r0.eff+r0.profile[j].Offset > t
	}) - 1
	if j >= 0 && r0.profile[j].Cost > 0 {
		bestArr, bestDep = t+r0.profile[j].Cost, t
	}
	scan := func(r *record, j0 int) {
		for j := j0; j < len(r.profile); j++ {
			seg := &r.profile[j]
			start := r.eff + seg.Offset
			if start >= bestArr {
				// Later starts can only arrive later.
				break
			}
			if seg.Cost > 0 && start+seg.Cost < bestArr {
				bestArr, bestDep = start+seg.Cost, start
			}
		}
	}
	scan(r0, j+1)
	for ri := ri0 + 1; ri < len(recs); ri++ {
		if recs[ri].eff >= bestArr {
			break
		}
		scan(&recs[ri], 0)
	}
	if bestArr == math.MaxInt64 {
		return 0, 0, false
	}
	return bestArr, bestDep, true
}

// pqItem is a priority-queue entry; ties break by node id so that
// runs are exactly reproducible.
type pqItem struct {
	dist int64
	node int
}

type pq []pqItem

func (h pq) Len() int { return len(h) }
func (h pq) Less(i, j int) bool {
	if h[i].dist != h[j].dist {
		return h[i].dist < h[j].dist
	}
	return h[i].node < h[j].node
}
func (h pq) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *pq) Push(x any)   { *h = append(*h, x.(pqItem)) }
func (h *pq) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// EarliestArrival computes the earliest arrival at g when leaving s
// at time t0, with arbitrary waiting allowed at every node. When ver
// is nil the current version is used; otherwise only edges and
// records visible at version *ver are considered, so the same (s, t0,
// g, ver) query always returns the identical result.
//
// Failure order: ErrInvalidParam, ErrVersionFuture, ErrUnreachable.
func (nt *Net) EarliestArrival(s int, t0 int64, g int, ver *int64) (Result, error) {
	if !nt.validNode(s) || !nt.validNode(g) {
		return Result{}, fmt.Errorf("%w: endpoints (%d,%d) outside [0,%d)", ErrInvalidParam, s, g, nt.n)
	}
	if !validTime(t0) {
		return Result{}, fmt.Errorf("%w: departure time %d outside [0,%d]", ErrInvalidParam, t0, MaxTime)
	}
	if ver != nil && *ver < 0 {
		return Result{}, fmt.Errorf("%w: negative version %d", ErrInvalidParam, *ver)
	}
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	v := nt.version
	if ver != nil {
		v = *ver
	}
	if v > nt.version {
		return Result{}, fmt.Errorf("%w: version %d > current %d", ErrVersionFuture, v, nt.version)
	}

	const inf = int64(math.MaxInt64)
	d := make([]int64, nt.n)
	for i := range d {
		d[i] = inf
	}
	d[s] = t0
	recCache := make(map[int][]record)
	visible := func(e *edge) ([]record, bool) {
		if e.regVer > v {
			return nil, false
		}
		recs, ok := recCache[e.id]
		if !ok {
			recs = visibleRecords(e, v)
			recCache[e.id] = recs
		}
		return recs, true
	}

	queue := &pq{{dist: t0, node: s}}
	heap.Init(queue)
	popped := 0
	for queue.Len() > 0 {
		it := heap.Pop(queue).(pqItem)
		if it.dist > d[it.node] {
			continue // stale entry
		}
		popped++
		if it.node == g {
			break
		}
		for _, eid := range nt.adj[it.node] {
			e := nt.edges[eid-1]
			recs, ok := visible(e)
			if !ok {
				continue
			}
			arr, _, ok := evalEdge(recs, d[it.node])
			if ok && arr < d[e.v] {
				d[e.v] = arr
				heap.Push(queue, pqItem{dist: arr, node: e.v})
			}
		}
	}
	if d[g] == inf {
		return Result{}, fmt.Errorf("%w: no path from %d to %d at version %d", ErrUnreachable, s, g, v)
	}

	legs := nt.buildRoute(s, g, d, v, visible)
	return Result{Arrival: d[g], Legs: legs, Popped: popped, Version: v}, nil
}

// buildRoute reconstructs the route over tight edges: an edge (a,b)
// is tight when arr_e(d(a)) == d(b). Among tight s->g paths it takes
// the fewest edges, then the lexicographically smallest edge id
// sequence (greedy per position; each chosen prefix keeps a full
// k-leg completion possible).
func (nt *Net) buildRoute(s, g int, d []int64, ver int64, visible func(*edge) ([]record, bool)) []Leg {
	const inf = int64(math.MaxInt64)
	// Evaluate every tight edge once, keyed by edge id.
	arrOf := make(map[int]int64)
	depOf := make(map[int]int64)
	tight := func(e *edge) bool {
		if d[e.u] == inf {
			return false
		}
		arr, ok := arrOf[e.id]
		if !ok {
			recs, vis := visible(e)
			if !vis {
				arrOf[e.id] = -1
				return false
			}
			a, dep, ok2 := evalEdge(recs, d[e.u])
			if !ok2 {
				arrOf[e.id] = -1
				return false
			}
			arr, arrOf[e.id], depOf[e.id] = a, a, dep
		}
		return arr == d[e.v]
	}

	// Tight edges point from smaller to larger d (costs >= 1), so
	// dynamic programming over nodes ordered by d is acyclic.
	order := make([]int, 0, nt.n)
	for x := 0; x < nt.n; x++ {
		if d[x] != inf {
			order = append(order, x)
		}
	}
	sort.Slice(order, func(i, j int) bool { return d[order[i]] < d[order[j]] })

	hop := make([]int, nt.n)
	toGoal := make([]int, nt.n)
	for i := range hop {
		hop[i] = -1
		toGoal[i] = -1
	}
	hop[s] = 0
	for _, x := range order { // ascending d
		if hop[x] < 0 {
			continue
		}
		for _, eid := range nt.adj[x] {
			e := nt.edges[eid-1]
			if tight(e) && (hop[e.v] < 0 || hop[x]+1 < hop[e.v]) {
				hop[e.v] = hop[x] + 1
			}
		}
	}
	toGoal[g] = 0
	for i := len(order) - 1; i >= 0; i-- { // descending d
		x := order[i]
		for _, eid := range nt.adj[x] {
			e := nt.edges[eid-1]
			if tight(e) && toGoal[e.v] >= 0 && (toGoal[x] < 0 || toGoal[e.v]+1 < toGoal[x]) {
				toGoal[x] = toGoal[e.v] + 1
			}
		}
	}

	k := hop[g]
	legs := make([]Leg, 0, k)
	cur := s
	for step := 1; step <= k; step++ {
		bestID := 0
		bestTo := -1
		for _, eid := range nt.adj[cur] {
			e := nt.edges[eid-1]
			if !tight(e) || hop[e.v] != step || toGoal[e.v] != k-step {
				continue
			}
			if bestID == 0 || eid < bestID {
				bestID, bestTo = eid, e.v
			}
		}
		legs = append(legs, Leg{EdgeID: bestID, Depart: depOf[bestID], Arrive: d[bestTo]})
		cur = bestTo
	}
	return legs
}
