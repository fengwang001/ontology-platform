// Package dbscan implements an incremental sliding-window DBSCAN clustering
// service over a stream of two-dimensional integer points.
//
// Points are inserted with a birth time equal to the service clock, removed
// manually, or expire when the clock reaches birth+W (the window is
// left-closed, right-open: a point is alive iff now < birth+W). The service
// maintains, for every alive point, its core/border/noise role and a
// deterministic cluster label, and reports the exact label changes and
// cluster events caused by every accepted operation.
//
// All methods are safe for concurrent use; the result of concurrent calls is
// equivalent to some serial order.
package dbscan

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	// maxCoord is the inclusive bound for |x| and |y|.
	maxCoord = 1_000_000
	// maxTime is the inclusive upper bound for Tick arguments.
	maxTime = 1_000_000_000_000_000
)

// Distinguishable rejection reasons returned by the mutating operations.
var (
	ErrInvalidParam = errors.New("dbscan: invalid parameter")
	ErrDuplicateID  = errors.New("dbscan: point id already exists")
	ErrCapacityFull = errors.New("dbscan: alive point capacity reached")
	ErrNotFound     = errors.New("dbscan: point id not found")
	ErrClockBack    = errors.New("dbscan: clock cannot move backwards")
)

// Change describes one point whose label changed because of an operation.
// OldLabel is -1 when the point did not exist before the operation, and
// NewLabel is -1 when the point does not exist after it.
type Change struct {
	ID       int
	OldLabel int
	NewLabel int
}

// EventType classifies a cluster event.
type EventType int

const (
	Birth EventType = iota
	Death
	Relabel
	Merge
	Split
	Reshape
)

func (t EventType) String() string {
	switch t {
	case Birth:
		return "Birth"
	case Death:
		return "Death"
	case Relabel:
		return "Relabel"
	case Merge:
		return "Merge"
	case Split:
		return "Split"
	case Reshape:
		return "Reshape"
	}
	return "Unknown"
}

// Event describes one connected component of the bipartite graph between
// the clusters before and after an operation, where an edge means the two
// clusters share at least one core point alive in both states.
type Event struct {
	Type      EventType
	OldLabels []int
	NewLabels []int
}

// Result is returned by every accepted mutating operation.
type Result struct {
	Changes []Change
	Events  []Event
}

// Cluster is one labelled cluster: its label and its sorted member ids
// (core and border points; noise is not part of any cluster).
type Cluster struct {
	Label   int
	Members []int
}

// point is one alive point with its cached neighborhood.
type point struct {
	id    int
	x, y  int64
	birth int64
	nb    map[int]struct{} // ids within eps, including itself
	core  bool
	label int // cluster label, 0 = noise
}

// cell is a grid cell coordinate of the spatial index.
type cell struct {
	x, y int64
}

// expiryItem schedules the expiration of one point.
type expiryItem struct {
	expiry int64 // birth + W
	id     int
}

// expiryHeap is a min-heap of expiryItem ordered by expiry time.
type expiryHeap []expiryItem

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].expiry < h[j].expiry }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)        { *h = append(*h, x.(expiryItem)) }
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// DBSCAN is the clustering service. The zero value is not usable; use New.
type DBSCAN struct {
	mu     sync.Mutex
	eps    int64
	eps2   int64
	minPts int
	window int64
	cap    int
	now    int64
	points map[int]*point
	grid   map[cell]map[int]struct{}
	expiry expiryHeap
	// rangeQueries counts the eps-neighborhood range queries issued against
	// the spatial index. It proves updates stay local: inserts issue exactly
	// one query, removals and ticks issue none.
	rangeQueries int
}

// New constructs the service. It returns ErrInvalidParam when eps is outside
// [1,1e6], minPts outside [1,1000], window outside [1,1e9] or capacity
// outside [1,100000].
func New(eps, minPts int, window int64, capacity int) (*DBSCAN, error) {
	if eps < 1 || eps > 1_000_000 {
		return nil, fmt.Errorf("%w: eps %d outside [1,1000000]", ErrInvalidParam, eps)
	}
	if minPts < 1 || minPts > 1000 {
		return nil, fmt.Errorf("%w: minPts %d outside [1,1000]", ErrInvalidParam, minPts)
	}
	if window < 1 || window > 1_000_000_000 {
		return nil, fmt.Errorf("%w: window %d outside [1,1000000000]", ErrInvalidParam, window)
	}
	if capacity < 1 || capacity > 100_000 {
		return nil, fmt.Errorf("%w: capacity %d outside [1,100000]", ErrInvalidParam, capacity)
	}
	e := int64(eps)
	return &DBSCAN{
		eps:    e,
		eps2:   e * e,
		minPts: minPts,
		window: window,
		cap:    capacity,
		points: make(map[int]*point),
		grid:   make(map[cell]map[int]struct{}),
	}, nil
}

// floorDiv returns floor(a/b) for positive b, correct for negative a.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// rangeQuery returns all alive points within eps of (x,y). It is the only
// operation accounted by the rangeQueries counter.
func (d *DBSCAN) rangeQuery(x, y int64) []*point {
	d.rangeQueries++
	cx0, cx1 := floorDiv(x-d.eps, d.eps), floorDiv(x+d.eps, d.eps)
	cy0, cy1 := floorDiv(y-d.eps, d.eps), floorDiv(y+d.eps, d.eps)
	var out []*point
	for cx := cx0; cx <= cx1; cx++ {
		for cy := cy0; cy <= cy1; cy++ {
			for id := range d.grid[cell{cx, cy}] {
				p := d.points[id]
				dx, dy := p.x-x, p.y-y
				if dx*dx+dy*dy <= d.eps2 {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// addPoint inserts p (whose nb only contains itself) into all indexes and
// links it with the neighbors found by rangeQuery.
func (d *DBSCAN) addPoint(p *point, nbrs []*point) {
	d.points[p.id] = p
	for _, q := range nbrs {
		p.nb[q.id] = struct{}{}
		q.nb[p.id] = struct{}{}
	}
	c := cell{floorDiv(p.x, d.eps), floorDiv(p.y, d.eps)}
	m := d.grid[c]
	if m == nil {
		m = make(map[int]struct{})
		d.grid[c] = m
	}
	m[p.id] = struct{}{}
	heap.Push(&d.expiry, expiryItem{expiry: p.birth + d.window, id: p.id})
}

// deletePoint removes p from all indexes and from its neighbors' lists.
func (d *DBSCAN) deletePoint(p *point) {
	for nid := range p.nb {
		if nid != p.id {
			delete(d.points[nid].nb, p.id)
		}
	}
	delete(d.points, p.id)
	c := cell{floorDiv(p.x, d.eps), floorDiv(p.y, d.eps)}
	delete(d.grid[c], p.id)
	if len(d.grid[c]) == 0 {
		delete(d.grid, c)
	}
}

// Label returns the cluster label of an alive point, 0 for noise and -1
// when no alive point has this id.
func (d *DBSCAN) Label(id int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.points[id]; ok {
		return p.label
	}
	return -1
}

// Neighbors returns the sorted ids of the eps-neighborhood of id (including
// id itself), or nil when id is not alive.
func (d *DBSCAN) Neighbors(id int) []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.points[id]
	if !ok {
		return nil
	}
	out := make([]int, 0, len(p.nb))
	for nid := range p.nb {
		out = append(out, nid)
	}
	sort.Ints(out)
	return out
}

// Clusters returns all clusters ordered by label; members of each cluster
// are sorted by id. Noise points appear in no cluster.
func (d *DBSCAN) Clusters() []Cluster {
	d.mu.Lock()
	defer d.mu.Unlock()
	byLabel := make(map[int][]int)
	for _, p := range d.points {
		if p.label > 0 {
			byLabel[p.label] = append(byLabel[p.label], p.id)
		}
	}
	labels := make([]int, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	sort.Ints(labels)
	out := make([]Cluster, 0, len(labels))
	for _, l := range labels {
		members := byLabel[l]
		sort.Ints(members)
		out = append(out, Cluster{Label: l, Members: members})
	}
	return out
}

// Alive returns the number of alive points.
func (d *DBSCAN) Alive() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.points)
}

// Now returns the current value of the service clock.
func (d *DBSCAN) Now() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.now
}
