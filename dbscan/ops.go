package dbscan

import (
	"container/heap"
	"fmt"
	"sort"
)

// clusterView is a snapshot of one cluster: its label and the ids of its
// core points.
type clusterView struct {
	label int
	cores map[int]struct{}
}

// Insert adds a point born at the current clock value. Rejections are
// checked in the order: invalid parameter, duplicate id, capacity full.
func (d *DBSCAN) Insert(id, x, y int) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id <= 0 {
		return Result{}, fmt.Errorf("%w: id %d is not positive", ErrInvalidParam, id)
	}
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return Result{}, fmt.Errorf("%w: coordinate (%d,%d) outside [-%d,%d]", ErrInvalidParam, x, y, maxCoord, maxCoord)
	}
	if _, ok := d.points[id]; ok {
		return Result{}, ErrDuplicateID
	}
	if len(d.points) >= d.cap {
		return Result{}, ErrCapacityFull
	}
	return d.insert(id, int64(x), int64(y)), nil
}

// Remove deletes an alive point.
func (d *DBSCAN) Remove(id int) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id <= 0 {
		return Result{}, fmt.Errorf("%w: id %d is not positive", ErrInvalidParam, id)
	}
	if _, ok := d.points[id]; !ok {
		return Result{}, ErrNotFound
	}
	return d.removeBatch([]int{id}), nil
}

// Tick advances the clock to t and expires every point with birth+W <= t.
// t == now is allowed and only re-runs the expiration check.
func (d *DBSCAN) Tick(t int64) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t > maxTime {
		return Result{}, fmt.Errorf("%w: tick time %d exceeds %d", ErrInvalidParam, t, maxTime)
	}
	if t < d.now {
		return Result{}, ErrClockBack
	}
	d.now = t
	var expired []int
	seen := map[int]struct{}{}
	for len(d.expiry) > 0 && d.expiry[0].expiry <= t {
		it := heap.Pop(&d.expiry).(expiryItem)
		if _, dup := seen[it.id]; dup {
			continue
		}
		if p, ok := d.points[it.id]; ok && p.birth+d.window == it.expiry {
			seen[it.id] = struct{}{}
			expired = append(expired, it.id)
		}
	}
	if len(expired) == 0 {
		return Result{}, nil
	}
	return d.removeBatch(expired), nil
}

// insert performs the accepted insertion of (id,x,y) born at d.now.
func (d *DBSCAN) insert(id int, x, y int64) Result {
	nbrs := d.rangeQuery(x, y)
	p := &point{id: id, x: x, y: y, birth: d.now, nb: map[int]struct{}{id: {}}}

	// Determine which points become core because of this insertion, before
	// mutating any state.
	newCores := map[int]struct{}{}
	if len(nbrs)+1 >= d.minPts {
		newCores[id] = struct{}{}
	}
	for _, q := range nbrs {
		if !q.core && len(q.nb)+1 >= d.minPts {
			newCores[q.id] = struct{}{}
		}
	}

	if len(newCores) == 0 {
		// No core point appears: clusters are unchanged, p is border or
		// noise and takes the smallest label among its core neighbors.
		label := 0
		for _, q := range nbrs {
			if q.core && (label == 0 || q.label < label) {
				label = q.label
			}
		}
		p.label = label
		d.addPoint(p, nbrs)
		return Result{Changes: []Change{{ID: id, OldLabel: -1, NewLabel: label}}}
	}

	// Old clusters touched by the insertion: every old core adjacent (in
	// the after-state graph) to a new core belongs to an affected cluster.
	seeds := map[int]struct{}{}
	for qid := range newCores {
		if qid == id {
			for _, q := range nbrs {
				if q.core {
					seeds[q.id] = struct{}{}
				}
			}
			continue
		}
		for rid := range d.points[qid].nb {
			if d.points[rid].core {
				seeds[rid] = struct{}{}
			}
		}
	}
	oldClusters := d.collectClusters(seeds)

	// Points whose label may change: neighbors of affected old cores,
	// neighbors of new cores, and p itself.
	relabel := map[int]struct{}{id: {}}
	for _, c := range oldClusters {
		for cid := range c.cores {
			for rid := range d.points[cid].nb {
				relabel[rid] = struct{}{}
			}
		}
	}
	for qid := range newCores {
		if qid != id {
			for rid := range d.points[qid].nb {
				relabel[rid] = struct{}{}
			}
		}
	}
	for _, q := range nbrs {
		relabel[q.id] = struct{}{}
	}
	oldLabels := map[int]int{id: -1}
	for rid := range relabel {
		if rid != id {
			oldLabels[rid] = d.points[rid].label
		}
	}

	// Mutate: insert p, link neighborhoods, promote new cores.
	d.addPoint(p, nbrs)
	for qid := range newCores {
		d.points[qid].core = true
	}

	// Recompute connected components over the affected cores only. Cores
	// outside this set cannot be adjacent to any core inside it.
	uCores := map[int]struct{}{}
	for qid := range newCores {
		uCores[qid] = struct{}{}
	}
	for _, c := range oldClusters {
		for cid := range c.cores {
			uCores[cid] = struct{}{}
		}
	}
	newClusters := d.components(uCores)
	for _, comp := range newClusters {
		for cid := range comp.cores {
			d.points[cid].label = comp.label
		}
	}
	for rid := range relabel {
		r := d.points[rid]
		if r.core {
			continue
		}
		r.label = d.borderLabel(r)
	}

	changes := make([]Change, 0, len(oldLabels))
	for rid, old := range oldLabels {
		if d.points[rid].label != old {
			changes = append(changes, Change{ID: rid, OldLabel: old, NewLabel: d.points[rid].label})
		}
	}
	sortChanges(changes)
	return Result{Changes: changes, Events: computeEvents(oldClusters, newClusters)}
}

// removeBatch deletes a set of alive points as one atomic step (used by
// Remove and by Tick for simultaneous expirations).
func (d *DBSCAN) removeBatch(ids []int) Result {
	removed := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		removed[id] = struct{}{}
	}

	// Points whose core status can change are the removed points and their
	// neighbors. Count how many removed points each neighbor loses.
	lossCount := map[int]int{}
	seeds := map[int]struct{}{}
	for _, id := range ids {
		p := d.points[id]
		if p.core {
			seeds[id] = struct{}{}
		}
		for nid := range p.nb {
			lossCount[nid]++
		}
	}
	for nid, cnt := range lossCount {
		if _, gone := removed[nid]; gone {
			continue
		}
		if q := d.points[nid]; q.core && len(q.nb)-cnt < d.minPts {
			seeds[nid] = struct{}{}
		}
	}

	if len(seeds) == 0 {
		// No core point is removed or demoted: clusters are unchanged.
		changes := make([]Change, 0, len(ids))
		for _, id := range ids {
			changes = append(changes, Change{ID: id, OldLabel: d.points[id].label, NewLabel: -1})
		}
		for _, id := range ids {
			d.deletePoint(d.points[id])
		}
		sortChanges(changes)
		return Result{Changes: changes}
	}

	oldClusters := d.collectClusters(seeds)

	// Points whose label may change: neighbors of affected cores (this
	// includes every member of the affected clusters), plus the removed
	// points themselves for the change report.
	relabel := map[int]struct{}{}
	for _, c := range oldClusters {
		for cid := range c.cores {
			for rid := range d.points[cid].nb {
				relabel[rid] = struct{}{}
			}
		}
	}
	oldLabels := make(map[int]int, len(relabel)+len(ids))
	for rid := range relabel {
		oldLabels[rid] = d.points[rid].label
	}
	for _, id := range ids {
		if _, ok := oldLabels[id]; !ok {
			oldLabels[id] = d.points[id].label
		}
	}

	// Mutate: delete the points, then recompute the core flag of every
	// point whose neighborhood shrank.
	for _, id := range ids {
		d.deletePoint(d.points[id])
	}
	for nid := range lossCount {
		if _, gone := removed[nid]; gone {
			continue
		}
		q := d.points[nid]
		q.core = len(q.nb) >= d.minPts
	}

	// Recompute connected components over the surviving affected cores.
	uCores := map[int]struct{}{}
	for _, c := range oldClusters {
		for cid := range c.cores {
			if _, gone := removed[cid]; gone {
				continue
			}
			if d.points[cid].core {
				uCores[cid] = struct{}{}
			}
		}
	}
	newClusters := d.components(uCores)
	for _, comp := range newClusters {
		for cid := range comp.cores {
			d.points[cid].label = comp.label
		}
	}
	for rid := range relabel {
		if _, gone := removed[rid]; gone {
			continue
		}
		r := d.points[rid]
		if r.core {
			continue
		}
		r.label = d.borderLabel(r)
	}

	changes := make([]Change, 0, len(oldLabels))
	for rid, old := range oldLabels {
		newLabel := -1
		if _, gone := removed[rid]; !gone {
			newLabel = d.points[rid].label
		}
		if newLabel != old {
			changes = append(changes, Change{ID: rid, OldLabel: old, NewLabel: newLabel})
		}
	}
	sortChanges(changes)
	return Result{Changes: changes, Events: computeEvents(oldClusters, newClusters)}
}

// borderLabel computes the label of a non-core point: the smallest label
// among its core neighbors, or 0 (noise) when it has none.
func (d *DBSCAN) borderLabel(p *point) int {
	label := 0
	for nid := range p.nb {
		n := d.points[nid]
		if n.core && (label == 0 || n.label < label) {
			label = n.label
		}
	}
	return label
}

// collectClusters runs a BFS over the current core-point graph starting
// from the given seed cores and returns one clusterView per connected
// component reached. The label is the smallest core id of the component,
// which equals the stored label by the labeling invariant.
func (d *DBSCAN) collectClusters(seeds map[int]struct{}) []clusterView {
	visited := make(map[int]struct{}, len(seeds))
	var out []clusterView
	for sid := range seeds {
		if _, ok := visited[sid]; ok {
			continue
		}
		cores := map[int]struct{}{}
		queue := []int{sid}
		visited[sid] = struct{}{}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			cores[cur] = struct{}{}
			for nid := range d.points[cur].nb {
				if _, ok := visited[nid]; ok {
					continue
				}
				if d.points[nid].core {
					visited[nid] = struct{}{}
					queue = append(queue, nid)
				}
			}
		}
		out = append(out, clusterView{label: minCore(cores), cores: cores})
	}
	return out
}

// components computes the connected components of the core-point graph
// restricted to the given set of core ids. Every component gets the
// smallest of its core ids as label.
func (d *DBSCAN) components(u map[int]struct{}) []clusterView {
	visited := make(map[int]struct{}, len(u))
	var out []clusterView
	for sid := range u {
		if _, ok := visited[sid]; ok {
			continue
		}
		cores := map[int]struct{}{}
		queue := []int{sid}
		visited[sid] = struct{}{}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			cores[cur] = struct{}{}
			for nid := range d.points[cur].nb {
				if _, ok := u[nid]; !ok {
					continue
				}
				if _, ok := visited[nid]; ok {
					continue
				}
				visited[nid] = struct{}{}
				queue = append(queue, nid)
			}
		}
		out = append(out, clusterView{label: minCore(cores), cores: cores})
	}
	return out
}

// minCore returns the smallest id in a non-empty core set.
func minCore(cores map[int]struct{}) int {
	min := 0
	for id := range cores {
		if min == 0 || id < min {
			min = id
		}
	}
	return min
}

// sortChanges orders a change report by point id.
func sortChanges(changes []Change) {
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
}

// computeEvents diffs the clusters before and after an operation. Two
// clusters are linked when they share at least one core point alive in
// both states; every connected component of this bipartite graph yields at
// most one event. Events are ordered by the smallest involved label.
func computeEvents(oldCls, newCls []clusterView) []Event {
	if len(oldCls) == 0 && len(newCls) == 0 {
		return nil
	}
	type node struct {
		side  int // 0 = old cluster, 1 = new cluster
		label int
	}
	parent := map[node]node{}
	var find func(n node) node
	find = func(n node) node {
		p, ok := parent[n]
		if !ok {
			parent[n] = n
			return n
		}
		if p == n {
			return n
		}
		root := find(p)
		parent[n] = root
		return root
	}
	join := func(a, b node) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	oldOf := map[int]int{}
	for _, c := range oldCls {
		for id := range c.cores {
			oldOf[id] = c.label
		}
	}
	for _, c := range newCls {
		for id := range c.cores {
			if ol, ok := oldOf[id]; ok {
				join(node{0, ol}, node{1, c.label})
			}
		}
	}

	type group struct {
		olds map[int]struct{}
		news map[int]struct{}
	}
	groups := map[node]*group{}
	get := func(n node) *group {
		root := find(n)
		g, ok := groups[root]
		if !ok {
			g = &group{olds: map[int]struct{}{}, news: map[int]struct{}{}}
			groups[root] = g
		}
		return g
	}
	for _, c := range oldCls {
		get(node{0, c.label}).olds[c.label] = struct{}{}
	}
	for _, c := range newCls {
		get(node{1, c.label}).news[c.label] = struct{}{}
	}

	var events []Event
	for _, g := range groups {
		olds := sortedSet(g.olds)
		news := sortedSet(g.news)
		var ev Event
		switch {
		case len(olds) == 0:
			ev = Event{Type: Birth, NewLabels: news}
		case len(news) == 0:
			ev = Event{Type: Death, OldLabels: olds}
		case len(olds) == 1 && len(news) == 1 && olds[0] == news[0]:
			continue // same cluster, membership changed only
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
	sort.Slice(events, func(i, j int) bool {
		return eventLess(events[i], events[j])
	})
	return events
}

// eventLess orders events deterministically: by the smallest involved
// label, then by the old label list, then by the new label list (an empty
// list sorts first). The tie-breakers only matter when a Birth and a Death
// share the same smallest label.
func eventLess(a, b Event) bool {
	if am, bm := eventMinLabel(a), eventMinLabel(b); am != bm {
		return am < bm
	}
	if c := compareInts(a.OldLabels, b.OldLabels); c != 0 {
		return c < 0
	}
	return compareInts(a.NewLabels, b.NewLabels) < 0
}

// compareInts lexicographically compares two sorted int slices; a shorter
// common prefix sorts first.
func compareInts(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
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

// eventMinLabel is the primary sort key of an event: the smallest involved
// label.
func eventMinLabel(ev Event) int {
	min := 0
	for _, l := range ev.OldLabels {
		if min == 0 || l < min {
			min = l
		}
	}
	for _, l := range ev.NewLabels {
		if min == 0 || l < min {
			min = l
		}
	}
	return min
}

// sortedSet returns the sorted elements of an int set.
func sortedSet(s map[int]struct{}) []int {
	out := make([]int, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}
