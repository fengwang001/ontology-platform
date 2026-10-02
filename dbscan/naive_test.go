package dbscan

import (
	"sort"
)

// naivePoint is one alive point in the reference simulator.
type naivePoint struct {
	x, y  int64
	birth int64
}

// naiveSim mirrors the service contract with a whole-sale recomputation
// after every operation. It shares no state with DBSCAN.
type naiveSim struct {
	eps, eps2 int64
	minPts    int
	window    int64
	cap       int
	now       int64
	pts       map[int]naivePoint
}

func newNaiveSim(eps, minPts int, window int64, capacity int) *naiveSim {
	return &naiveSim{
		eps:    int64(eps),
		eps2:   int64(eps) * int64(eps),
		minPts: minPts,
		window: window,
		cap:    capacity,
		pts:    map[int]naivePoint{},
	}
}

// naiveState is a full clustering of one point set.
type naiveState struct {
	labels map[int]int          // id -> label (0 = noise)
	cores  map[int]map[int]bool // cluster label -> core id set
	nb     map[int][]int        // id -> sorted neighbor ids (incl. self)
}

// recluster computes labels and clusters from scratch.
func (s *naiveSim) recluster() naiveState {
	ids := make([]int, 0, len(s.pts))
	for id := range s.pts {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	st := naiveState{labels: map[int]int{}, cores: map[int]map[int]bool{}, nb: map[int][]int{}}
	core := map[int]bool{}
	for _, id := range ids {
		p := s.pts[id]
		var nb []int
		for _, oid := range ids {
			q := s.pts[oid]
			dx, dy := p.x-q.x, p.y-q.y
			if dx*dx+dy*dy <= s.eps2 {
				nb = append(nb, oid)
			}
		}
		st.nb[id] = nb
		core[id] = len(nb) >= s.minPts
	}

	// Connected components of core points under mutual neighborhood.
	comp := map[int]int{} // core id -> component representative (smallest id)
	var find func(map[int]int, int) int
	find = func(parent map[int]int, x int) int {
		for parent[x] != x {
			x = parent[x]
		}
		return x
	}
	parent := map[int]int{}
	for _, id := range ids {
		if core[id] {
			parent[id] = id
		}
	}
	for _, id := range ids {
		if !core[id] {
			continue
		}
		for _, oid := range st.nb[id] {
			if !core[oid] {
				continue
			}
			ra, rb := find(parent, id), find(parent, oid)
			if ra != rb {
				if ra < rb {
					parent[rb] = ra
				} else {
					parent[ra] = rb
				}
			}
		}
	}
	for id := range parent {
		label := find(parent, id)
		comp[id] = label
		if st.cores[label] == nil {
			st.cores[label] = map[int]bool{}
		}
		st.cores[label][id] = true
	}

	for _, id := range ids {
		if core[id] {
			st.labels[id] = comp[id]
			continue
		}
		label := 0
		for _, oid := range st.nb[id] {
			if core[oid] && (label == 0 || comp[oid] < label) {
				label = comp[oid]
			}
		}
		st.labels[id] = label
	}
	return st
}

// naiveEvents diffs two full clusterings with the bipartite construction
// from the specification, implemented independently (BFS over the graph).
func naiveEvents(oldCores, newCores map[int]map[int]bool) []Event {
	type node struct {
		side  int
		label int
	}
	adj := map[node]map[node]bool{}
	link := func(a, b node) {
		if adj[a] == nil {
			adj[a] = map[node]bool{}
		}
		if adj[b] == nil {
			adj[b] = map[node]bool{}
		}
		adj[a][b] = true
		adj[b][a] = true
	}
	for ol, oc := range oldCores {
		for nl, nc := range newCores {
			shared := false
			for id := range oc {
				if nc[id] {
					shared = true
					break
				}
			}
			if shared {
				link(node{0, ol}, node{1, nl})
			}
		}
	}
	nodes := map[node]bool{}
	for l := range oldCores {
		nodes[node{0, l}] = true
	}
	for l := range newCores {
		nodes[node{1, l}] = true
	}

	visited := map[node]bool{}
	var events []Event
	for start := range nodes {
		if visited[start] {
			continue
		}
		var olds, news []int
		queue := []node{start}
		visited[start] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if cur.side == 0 {
				olds = append(olds, cur.label)
			} else {
				news = append(news, cur.label)
			}
			for next := range adj[cur] {
				if !visited[next] {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
		sort.Ints(olds)
		sort.Ints(news)
		var ev Event
		switch {
		case len(olds) == 0:
			ev = Event{Type: Birth, NewLabels: news}
		case len(news) == 0:
			ev = Event{Type: Death, OldLabels: olds}
		case len(olds) == 1 && len(news) == 1 && olds[0] == news[0]:
			continue
		case len(olds) == 1 && len(news) == 1:
			ev = Event{Type: Relabel, OldLabels: olds, NewLabels: news}
		case len(news) == 1:
			ev = Event{Type: Merge, OldLabels: olds, NewLabels: news}
		case len(olds) == 1:
			ev = Event{Type: Split, OldLabels: olds, NewLabels: news}
		default:
			ev = Event{Type: Reshape, OldLabels: olds, NewLabels: news}
		}
		events = append(events, ev)
	}
	sort.Slice(events, func(i, j int) bool { return eventLess(events[i], events[j]) })
	return events
}

// diffChanges computes the change report between two full states.
func diffChanges(before, after naiveState) []Change {
	ids := map[int]bool{}
	for id := range before.labels {
		ids[id] = true
	}
	for id := range after.labels {
		ids[id] = true
	}
	var changes []Change
	for id := range ids {
		oldL, okB := before.labels[id]
		if !okB {
			oldL = -1
		}
		newL, okA := after.labels[id]
		if !okA {
			newL = -1
		}
		if oldL != newL {
			changes = append(changes, Change{ID: id, OldLabel: oldL, NewLabel: newL})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	return changes
}

// apply mirrors one operation on the simulator and returns the expected
// result and rejection reason.
func (s *naiveSim) apply(op randomOp) (Result, error) {
	before := s.recluster()
	var err error
	switch op.kind {
	case opInsert:
		err = s.insert(op.id, op.x, op.y)
	case opRemove:
		err = s.remove(op.id)
	case opTick:
		err = s.tick(op.t)
	}
	if err != nil {
		return Result{}, err
	}
	after := s.recluster()
	return Result{
		Changes: diffChanges(before, after),
		Events:  naiveEvents(before.cores, after.cores),
	}, nil
}

func (s *naiveSim) insert(id, x, y int) error {
	if id <= 0 || x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return ErrInvalidParam
	}
	if _, ok := s.pts[id]; ok {
		return ErrDuplicateID
	}
	if len(s.pts) >= s.cap {
		return ErrCapacityFull
	}
	s.pts[id] = naivePoint{x: int64(x), y: int64(y), birth: s.now}
	return nil
}

func (s *naiveSim) remove(id int) error {
	if id <= 0 {
		return ErrInvalidParam
	}
	if _, ok := s.pts[id]; !ok {
		return ErrNotFound
	}
	delete(s.pts, id)
	return nil
}

func (s *naiveSim) tick(t int64) error {
	if t > maxTime {
		return ErrInvalidParam
	}
	if t < s.now {
		return ErrClockBack
	}
	s.now = t
	for id, p := range s.pts {
		if p.birth+s.window <= t {
			delete(s.pts, id)
		}
	}
	return nil
}
