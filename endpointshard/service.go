package endpointshard

import (
	"sort"
	"sync"
	"sync/atomic"
)

// endpointRef is the canonical, shared record of one endpoint. Shard
// membership maps and the consumer indexes all point at the same
// *endpointRef, so an in-place content update is visible everywhere
// without touching any shard structure.
type endpointRef struct {
	ep    Endpoint
	shard int
}

// shard is a numbered bucket of endpoints. gen is the modification
// generation: it is bumped exactly when the shard appears in a change
// report (creation counts).
type shard struct {
	num int
	gen uint64
	eps map[string]*endpointRef
}

// serviceState holds the whole mutable state of one service. All
// fields are guarded by mu; Sync/Resize take it for writing, Query and
// Inspect for reading, so a reader never observes an intermediate
// state of a mutation.
type serviceState struct {
	mu sync.RWMutex

	m       int
	nextNum int
	shards  map[int]*shard
	eps     map[string]*endpointRef

	// ready holds IDs of endpoints that are healthy and not
	// terminating; draining holds IDs of endpoints that are servable
	// and terminating (healthy and terminating). They are disjoint and
	// together cover exactly the healthy endpoints, so a Query touches
	// only endpoints that can appear in its result.
	ready    map[string]struct{}
	draining map[string]struct{}

	idx   sizeIndex
	stats *Stats
}

func newServiceState(m int, stats *Stats) *serviceState {
	return &serviceState{
		m:        m,
		nextNum:  1,
		shards:   make(map[int]*shard),
		eps:      make(map[string]*endpointRef),
		ready:    make(map[string]struct{}),
		draining: make(map[string]struct{}),
		idx:      sizeIndex{visits: &stats.SizeIndexNodeVisits},
		stats:    stats,
	}
}

// syncCtx accumulates the change report of one atomic adjustment
// (Sync or Resize).
type syncCtx struct {
	modified   map[int]bool
	deletedGen map[int]uint64
}

func newSyncCtx() *syncCtx {
	return &syncCtx{
		modified:   make(map[int]bool),
		deletedGen: make(map[int]uint64),
	}
}

func (s *serviceState) markModified(c *syncCtx, num int) {
	if !c.modified[num] {
		c.modified[num] = true
		s.stats.ShardsTouched.Add(1)
	}
}

// deleteShard removes a shard from the service. The shard keeps its
// bumped generation in the report via deletedGen.
func (s *serviceState) deleteShard(c *syncCtx, sh *shard) {
	s.idx.del(len(sh.eps), sh.num)
	delete(s.shards, sh.num)
	c.deletedGen[sh.num] = sh.gen + 1
	s.markModified(c, sh.num)
}

// resizeShard re-keys a shard in the size index around a mutation of
// its endpoint set.
func (s *serviceState) resizeShard(sh *shard, fn func()) {
	s.idx.del(len(sh.eps), sh.num)
	fn()
	s.idx.add(len(sh.eps), sh.num)
}

// reindex refreshes the consumer indexes for one endpoint.
func (s *serviceState) reindex(ref *endpointRef) {
	id := ref.ep.ID
	if ref.ep.Ready() {
		s.ready[id] = struct{}{}
	} else {
		delete(s.ready, id)
	}
	if ref.ep.Servable() && ref.ep.IsTerminating() {
		s.draining[id] = struct{}{}
	} else {
		delete(s.draining, id)
	}
}

// placeEndpoint inserts one new endpoint using the placement rule:
// the non-full shard with the most endpoints (ties to the lowest
// shard number), or a freshly created shard when none is non-full.
func (s *serviceState) placeEndpoint(c *syncCtx, ep Endpoint) {
	key, ok := s.idx.maxNonFull(s.m)
	var sh *shard
	if !ok {
		sh = &shard{num: s.nextNum, eps: make(map[string]*endpointRef)}
		s.nextNum++
		s.shards[sh.num] = sh
		s.idx.add(0, sh.num)
	} else {
		sh = s.shards[key.num]
	}
	ref := &endpointRef{ep: ep, shard: sh.num}
	s.resizeShard(sh, func() { sh.eps[ep.ID] = ref })
	s.eps[ep.ID] = ref
	s.reindex(ref)
	s.markModified(c, sh.num)
}

// removeEndpoint deletes one endpoint from its shard and the indexes.
func (s *serviceState) removeEndpoint(c *syncCtx, id string) {
	ref := s.eps[id]
	sh := s.shards[ref.shard]
	s.resizeShard(sh, func() { delete(sh.eps, id) })
	delete(s.eps, id)
	delete(s.ready, id)
	delete(s.draining, id)
	s.markModified(c, sh.num)
}

// syncLocked adjusts the shards to match desired exactly. desired is
// already validated by the caller.
func (s *serviceState) syncLocked(desired []Endpoint) *SyncReport {
	c := newSyncCtx()
	want := make(map[string]Endpoint, len(desired))
	for _, ep := range desired {
		want[ep.ID] = ep
	}

	// Removals: present now, absent from desired. Iterating the live
	// set costs O(|current|) = O(|desired| + |removed|), i.e. it is
	// bounded by the input size plus the actual change set.
	var removed []string
	for id := range s.eps {
		if _, ok := want[id]; !ok {
			removed = append(removed, id)
		}
	}
	sort.Strings(removed)
	for _, id := range removed {
		s.removeEndpoint(c, id)
	}

	// In-place content updates: present in both, content differs.
	// The endpoint never leaves its shard.
	var updated []string
	for id, ep := range want {
		if ref, ok := s.eps[id]; ok && ref.ep != ep {
			updated = append(updated, id)
		}
	}
	sort.Strings(updated)
	for _, id := range updated {
		ref := s.eps[id]
		ref.ep = want[id]
		s.reindex(ref)
		s.markModified(c, ref.shard)
	}

	// Additions: placed one by one in ascending ID order.
	var added []Endpoint
	for id, ep := range want {
		if _, ok := s.eps[id]; !ok {
			added = append(added, ep)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	for _, ep := range added {
		s.placeEndpoint(c, ep)
	}

	s.cleanup(c)
	return s.buildReport(c)
}

// cleanup performs the end-of-adjustment tidy-up: delete every empty
// shard, then merge at most once — the two shards with the fewest
// endpoints (ties to the lowest number) are merged when their sizes
// sum to at most m, moving every endpoint of the higher-numbered
// shard into the lower-numbered one.
func (s *serviceState) cleanup(c *syncCtx) {
	for {
		key, ok := s.idx.min()
		if !ok || key.size != 0 {
			break
		}
		s.deleteShard(c, s.shards[key.num])
	}

	pair, n := s.idx.twoMin()
	if n < 2 {
		return
	}
	a, b := pair[0], pair[1]
	if a.size+b.size > s.m {
		return
	}
	lo, hi := a, b
	if lo.num > hi.num {
		lo, hi = hi, lo
	}
	s.mergeShards(c, lo.num, hi.num)
}

// mergeShards moves every endpoint of hi into lo and deletes hi.
func (s *serviceState) mergeShards(c *syncCtx, loNum, hiNum int) {
	lo := s.shards[loNum]
	hi := s.shards[hiNum]
	s.resizeShard(lo, func() {
		for id, ref := range hi.eps {
			ref.shard = loNum
			lo.eps[id] = ref
		}
	})
	s.deleteShard(c, hi)
	s.markModified(c, loNum)
}

// buildReport bumps the generation of every reported shard and
// renders the report, sorted by shard number.
func (s *serviceState) buildReport(c *syncCtx) *SyncReport {
	nums := make([]int, 0, len(c.modified))
	for num := range c.modified {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	rep := &SyncReport{Changes: make([]ShardChange, 0, len(nums))}
	for _, num := range nums {
		if sh, ok := s.shards[num]; ok {
			sh.gen++
			rep.Changes = append(rep.Changes, ShardChange{
				ShardNum:   num,
				Generation: sh.gen,
				Endpoints:  sortedEndpoints(sh.eps),
			})
		} else {
			rep.Changes = append(rep.Changes, ShardChange{
				ShardNum:   num,
				Generation: c.deletedGen[num],
				Deleted:    true,
			})
		}
	}
	return rep
}

func sortedEndpoints(eps map[string]*endpointRef) []Endpoint {
	out := make([]Endpoint, 0, len(eps))
	for _, ref := range eps {
		out = append(out, ref.ep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// resizeLocked changes the shard capacity. Growing never moves
// anything. Shrinking evicts the lexicographically largest endpoints
// of over-capacity shards and re-places them with the placement rule,
// atomically, then tidies up exactly once.
func (s *serviceState) resizeLocked(newM int) *SyncReport {
	c := newSyncCtx()
	oldM := s.m
	s.m = newM
	if newM >= oldM {
		return s.buildReport(c)
	}

	// Collect over-capacity shards (only shards the shrink actually
	// affects are visited), then evict their largest-ID endpoints.
	over := s.idx.collectSizeGT(newM)
	sort.Slice(over, func(i, j int) bool { return over[i].num < over[j].num })
	var evicted []Endpoint
	for _, key := range over {
		sh := s.shards[key.num]
		ids := make([]string, 0, len(sh.eps))
		for id := range sh.eps {
			ids = append(ids, id)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
		ids = ids[:len(sh.eps)-newM]
		for _, id := range ids {
			evicted = append(evicted, s.eps[id].ep)
			s.removeEndpoint(c, id)
		}
	}

	// Re-place all evicted endpoints in ascending ID order.
	sort.Slice(evicted, func(i, j int) bool { return evicted[i].ID < evicted[j].ID })
	for _, ep := range evicted {
		s.placeEndpoint(c, ep)
	}

	s.cleanup(c)
	return s.buildReport(c)
}

// queryLocked answers a consumer read. It touches only endpoints that
// can appear in the result, so its cost is proportional to the result
// size (plus the final ordering pass).
func (s *serviceState) queryLocked(region string) *QueryResult {
	pool := s.ready
	fallback := false
	if len(pool) == 0 {
		pool = s.draining
		fallback = true
	}
	out := make([]Endpoint, 0, len(pool))
	for id := range pool {
		out = append(out, s.eps[id].ep)
	}
	s.stats.QueryEndpointsSeen.Add(int64(len(out)))
	sort.Slice(out, func(i, j int) bool {
		sameI := out[i].Region == region
		sameJ := out[j].Region == region
		if sameI != sameJ {
			return sameI
		}
		return out[i].ID < out[j].ID
	})
	return &QueryResult{Endpoints: out, Fallback: fallback}
}

// inspectLocked renders a consistent snapshot of all shards.
func (s *serviceState) inspectLocked() []ShardInfo {
	nums := make([]int, 0, len(s.shards))
	for num := range s.shards {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	out := make([]ShardInfo, 0, len(nums))
	for _, num := range nums {
		sh := s.shards[num]
		out = append(out, ShardInfo{
			Num:        num,
			Generation: sh.gen,
			Endpoints:  sortedEndpoints(sh.eps),
		})
	}
	return out
}

// Stats exposes operation counters so tests and benchmarks can prove
// the cost model without relying on wall-clock timing.
type Stats struct {
	// SizeIndexNodeVisits counts treap node visits; it grows with
	// O(changed shards * log live shards), never with untouched shards.
	SizeIndexNodeVisits atomic.Int64
	// ShardsTouched counts distinct shards modified by Sync/Resize.
	ShardsTouched atomic.Int64
	// QueryEndpointsSeen counts endpoints scanned by Query; it equals
	// the result size of each query.
	QueryEndpointsSeen atomic.Int64
}

// StatsSnapshot is a point-in-time copy of Stats.
type StatsSnapshot struct {
	SizeIndexNodeVisits int64
	ShardsTouched       int64
	QueryEndpointsSeen  int64
}

func (s *Stats) snapshot() StatsSnapshot {
	return StatsSnapshot{
		SizeIndexNodeVisits: s.SizeIndexNodeVisits.Load(),
		ShardsTouched:       s.ShardsTouched.Load(),
		QueryEndpointsSeen:  s.QueryEndpointsSeen.Load(),
	}
}

func (s *Stats) reset() {
	s.SizeIndexNodeVisits.Store(0)
	s.ShardsTouched.Store(0)
	s.QueryEndpointsSeen.Store(0)
}
