// Package consistenthash implements a consistent-hash ring with bounded
// loads. Keys are placed on the first clockwise point whose owning node is
// not yet full, and node removal / rebalancing re-place keys in a fixed,
// deterministic order.
package consistenthash

import (
	"container/heap"
	"errors"
	"math/bits"
	"sort"
	"sync"
)

// Predefined errors. The order in which operations report these errors is
// part of the package contract; rejected operations never mutate state.
var (
	ErrInvalidConfig = errors.New("consistenthash: invalid config: require 1 <= Cden <= Cnum <= 1000000")
	ErrInvalidKey    = errors.New("consistenthash: invalid key: empty key")
	ErrInvalidNode   = errors.New("consistenthash: invalid node argument")
	ErrNoNode        = errors.New("consistenthash: no node registered")
	ErrKeyExists     = errors.New("consistenthash: key already exists")
	ErrKeyNotFound   = errors.New("consistenthash: key not found")
	ErrNodeExists    = errors.New("consistenthash: node already exists")
	ErrPointConflict = errors.New("consistenthash: ring point conflicts with an existing point")
	ErrNodeNotFound  = errors.New("consistenthash: node not found")
	ErrLastNodeBusy  = errors.New("consistenthash: cannot remove the last node while it still holds keys")
	ErrInvalidLimit  = errors.New("consistenthash: invalid limit: must be within [0, 1e9]")
)

const maxPointsPerNode = 64

// pointEntry is one virtual node position on the ring.
type pointEntry struct {
	pos  uint64
	node int64
}

// keyRec is everything remembered about a placed key.
type keyRec struct {
	key  string
	pos  uint64
	seq  int64
	node int64
}

// keyMaxHeap yields the largest seq first; it is rebuilt lazily from the
// node's key map, so it never contains stale entries.
type keyMaxHeap []*keyRec

func (h keyMaxHeap) Len() int           { return len(h) }
func (h keyMaxHeap) Less(i, j int) bool { return h[i].seq > h[j].seq }
func (h keyMaxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *keyMaxHeap) Push(x any)        { *h = append(*h, x.(*keyRec)) }
func (h *keyMaxHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}

// node is one registered backend.
type node struct {
	id     int64
	points []uint64
	load   int
	keys   map[int64]*keyRec // keyed by seq
}

// Migration records one key move produced by Rebalance.
type Migration struct {
	Key  string
	From int64
	To   int64
}

// Ring is the concurrent-safe consistent hash ring.
type Ring struct {
	mu sync.Mutex

	cnum int64
	cden int64

	nodes  map[int64]*node
	points []pointEntry // always sorted by pos, globally unique

	keys map[string]*keyRec

	k    int // number of placed keys
	next int64

	// bisect instrumentation (non-exported). Every start-point binary
	// search increments total and records its comparison count in max.
	bisectTotal int64
	bisectMax   int
}

// New creates a Ring with load factor c = cnum/cden.
func New(cnum, cden int64) (*Ring, error) {
	if cden < 1 || cnum < cden || cnum > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Ring{
		cnum:   cnum,
		cden:   cden,
		nodes:  make(map[int64]*node),
		keys:   make(map[string]*keyRec),
		points: make([]pointEntry, 0),
	}, nil
}

// AddNode registers a node with the given ring points. It never moves keys.
func (r *Ring) AddNode(id int64, points []uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validation order: bad arguments -> duplicate node -> point conflict.
	if id < 1 || len(points) < 1 || len(points) > maxPointsPerNode {
		return ErrInvalidNode
	}
	seen := make(map[uint64]struct{}, len(points))
	for _, p := range points {
		if _, dup := seen[p]; dup {
			return ErrInvalidNode
		}
		seen[p] = struct{}{}
	}
	if _, exists := r.nodes[id]; exists {
		return ErrNodeExists
	}
	for _, p := range points {
		if _, conflict := r.pointOwner(p); conflict {
			return ErrPointConflict
		}
	}

	owned := make([]uint64, len(points))
	copy(owned, points)
	sort.Slice(owned, func(i, j int) bool { return owned[i] < owned[j] })
	r.nodes[id] = &node{
		id:     id,
		points: owned,
		keys:   make(map[int64]*keyRec),
	}

	for _, p := range points {
		r.points = append(r.points, pointEntry{pos: p, node: id})
	}
	sort.Slice(r.points, func(i, j int) bool { return r.points[i].pos < r.points[j].pos })
	return nil
}

// Put places a key, consuming a fresh seq on success only.
func (r *Ring) Put(key string, pos uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validation order: empty key -> no node -> duplicate key.
	if key == "" {
		return ErrInvalidKey
	}
	if len(r.nodes) == 0 {
		return ErrNoNode
	}
	if _, exists := r.keys[key]; exists {
		return ErrKeyExists
	}

	n := int64(len(r.nodes))
	cap := capOf(r.cnum, r.cden, int64(r.k), n)
	id, _ := r.placeOnce(pos, cap)

	r.next++
	rec := &keyRec{key: key, pos: pos, seq: r.next, node: id}
	r.keys[key] = rec
	nd := r.nodes[id]
	nd.load++
	nd.keys[rec.seq] = rec
	r.k++
	return nil
}

// Delete removes a key without moving anything else.
func (r *Ring) Delete(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if key == "" {
		return ErrInvalidKey
	}
	rec, ok := r.keys[key]
	if !ok {
		return ErrKeyNotFound
	}
	nd := r.nodes[rec.node]
	nd.load--
	delete(nd.keys, rec.seq)
	delete(r.keys, key)
	r.k--
	return nil
}

// Lookup returns the node owning a key.
func (r *Ring) Lookup(key string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if key == "" {
		return 0, ErrInvalidKey
	}
	rec, ok := r.keys[key]
	if !ok {
		return 0, ErrKeyNotFound
	}
	return rec.node, nil
}

// RemoveNode removes a node and re-places its keys in ascending seq order.
func (r *Ring) RemoveNode(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nd, ok := r.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if len(r.nodes) == 1 && nd.load > 0 {
		return ErrLastNodeBusy
	}

	// Detach the node's points first; remaining nodes own every ring point.
	removed := make(map[uint64]struct{}, len(nd.points))
	for _, p := range nd.points {
		removed[p] = struct{}{}
	}
	kept := r.points[:0]
	for _, pe := range r.points {
		if _, drop := removed[pe.pos]; !drop {
			kept = append(kept, pe)
		}
	}
	r.points = kept
	delete(r.nodes, id)

	// Take every key off the node, ordered by ascending seq for replay.
	victims := make([]*keyRec, 0, len(nd.keys))
	for _, rec := range nd.keys {
		victims = append(victims, rec)
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i].seq < victims[j].seq })
	r.k -= len(victims)

	n := int64(len(r.nodes))
	kc := int64(r.k)
	for _, rec := range victims {
		// cap uses the placed-so-far count at the moment of placement.
		cap := capOf(r.cnum, r.cden, kc, n)
		newID, _ := r.placeOnce(rec.pos, cap)
		rec.node = newID
		target := r.nodes[newID]
		target.load++
		target.keys[rec.seq] = rec
		kc++
	}
	r.k = int(kc)
	return nil
}

// Rebalance drains overloaded nodes and returns the migration log.
func (r *Ring) Rebalance(limit int) (migrations []Migration, excess int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if limit < 0 || limit > 1_000_000_000 {
		return nil, 0, ErrInvalidLimit
	}
	if len(r.nodes) == 0 {
		return nil, 0, ErrNoNode
	}

	n := int64(len(r.nodes))
	capR := capOfRebalance(r.cnum, r.cden, int64(r.k), n)

	ids := make([]int64, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	migrations = make([]Migration, 0)
	moved := 0

	for _, id := range ids {
		nd := r.nodes[id]
		for nd.load > int(capR) && moved < limit {
			rec := r.popMaxSeqKey(nd)

			// Detach first; with load >= capR the old node must be skipped.
			newID, _ := r.placeOnce(rec.pos, capR)
			rec.node = newID
			target := r.nodes[newID]
			target.load++
			target.keys[rec.seq] = rec

			migrations = append(migrations, Migration{Key: rec.key, From: id, To: newID})
			moved++
		}
		if moved >= limit {
			break
		}
	}

	for _, id := range ids {
		if l := r.nodes[id].load; l > int(capR) {
			excess += l - int(capR)
		}
	}
	return migrations, excess, nil
}

// Load returns a snapshot of node id -> load.
func (r *Ring) Load() map[int64]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[int64]int, len(r.nodes))
	for id, nd := range r.nodes {
		out[id] = nd.load
	}
	return out
}

// Seq returns the placement seq of a key.
func (r *Ring) Seq(key string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == "" {
		return 0, ErrInvalidKey
	}
	rec, ok := r.keys[key]
	if !ok {
		return 0, ErrKeyNotFound
	}
	return rec.seq, nil
}

// bisectStats returns (total bisect calls, max comparisons in one call).
func (r *Ring) bisectStats() (int64, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bisectTotal, r.bisectMax
}

// capOf computes ceil(cnum * (k+1) / (cden * n)).
func capOf(cnum, cden, k, n int64) int64 {
	num := cnum * (k + 1)
	den := cden * n
	return (num + den - 1) / den
}

// capOfRebalance computes ceil(cnum * k / (cden * n)); it is 0 when k == 0.
func capOfRebalance(cnum, cden, k, n int64) int64 {
	num := cnum * k
	den := cden * n
	return (num + den - 1) / den
}

// placeOnce locates the first clockwise non-full node starting at pos.
// start comparisons are counted. Returns node id and the chosen index.
func (r *Ring) placeOnce(pos uint64, cap int64) (int64, int) {
	start := r.locateStart(pos)
	p := len(r.points)
	for step := 0; step < p; step++ {
		idx := (start + step) % p
		id := r.points[idx].node
		if int64(r.nodes[id].load) < cap {
			return id, idx
		}
	}
	// Unreachable under the bounded-load invariant when cap is derived from
	// the current totals; guard keeps the contract explicit.
	return 0, -1
}

// locateStart returns the index of the first point with pos >= p in the
// sorted point slice, wrapping like lower_bound; -1 only when empty.
func (r *Ring) locateStart(p uint64) int {
	n := len(r.points)
	if n == 0 {
		return -1
	}
	// Hand-written binary search so comparisons can be counted: each loop
	// iteration performs exactly one key comparison.
	lo, hi := 0, n
	comps := 0
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		comps++
		if r.points[mid].pos < p {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == n { // wrap past the largest point
		lo = 0
	}
	r.bisectTotal++
	r.bisectMax = max(r.bisectMax, comps)
	return lo
}

// popMaxSeqKey removes and returns the key with the greatest seq on a node.
func (r *Ring) popMaxSeqKey(nd *node) *keyRec {
	h := make(keyMaxHeap, 0, len(nd.keys))
	for _, rec := range nd.keys {
		h = append(h, rec)
	}
	heap.Init(&h)
	rec := heap.Pop(&h).(*keyRec)
	nd.load--
	delete(nd.keys, rec.seq)
	return rec
}

// pointOwner reports whether a ring position already exists and its node.
func (r *Ring) pointOwner(p uint64) (int64, bool) {
	idx := sort.Search(len(r.points), func(i int) bool { return r.points[i].pos >= p })
	if idx < len(r.points) && r.points[idx].pos == p {
		return r.points[idx].node, true
	}
	return 0, false
}

// bisectBound is ceil(log2(p)) + 1, the proven per-lookup comparison bound.
func bisectBound(p int) int {
	if p <= 1 {
		return 1
	}
	return (bits.Len(uint(p - 1))) + 1
}
