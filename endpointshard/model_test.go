package endpointshard

import (
	"fmt"
	"reflect"
	"sort"
)

// This file contains an independent, deliberately naive implementation
// of the shard-maintenance semantics. It uses plain maps and full
// linear scans -- no size index, no incremental structures -- so that
// the randomized differential test can cross-check the optimized
// implementation step by step.

type naiveShard struct {
	num int
	gen uint64
	eps map[string]Endpoint
}

type naiveService struct {
	m       int
	nextNum int
	shards  map[int]*naiveShard
}

func newNaiveService(m int) *naiveService {
	return &naiveService{m: m, nextNum: 1, shards: make(map[int]*naiveShard)}
}

func (s *naiveService) sortedNums() []int {
	nums := make([]int, 0, len(s.shards))
	for n := range s.shards {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums
}

func (s *naiveService) findEndpoint(id string) *naiveShard {
	for _, sh := range s.shards {
		if _, ok := sh.eps[id]; ok {
			return sh
		}
	}
	return nil
}

// naivePlacement returns the fullest non-full shard (ties to the
// lowest number), creating one when every shard is full.
func (s *naiveService) naivePlacement() *naiveShard {
	var best *naiveShard
	for _, n := range s.sortedNums() {
		sh := s.shards[n]
		if len(sh.eps) < s.m && (best == nil || len(sh.eps) > len(best.eps)) {
			best = sh
		}
	}
	if best == nil {
		best = &naiveShard{num: s.nextNum, eps: make(map[string]Endpoint)}
		s.nextNum++
		s.shards[best.num] = best
	}
	return best
}

// naiveCleanup deletes all empty shards and merges at most one pair.
func (s *naiveService) naiveCleanup(modified map[int]bool, deletedGen map[int]uint64) {
	for _, n := range s.sortedNums() {
		sh := s.shards[n]
		if len(sh.eps) == 0 {
			delete(s.shards, n)
			deletedGen[n] = sh.gen + 1
			modified[n] = true
		}
	}
	nums := s.sortedNums()
	if len(nums) < 2 {
		return
	}
	sort.Slice(nums, func(i, j int) bool {
		a, b := s.shards[nums[i]], s.shards[nums[j]]
		if len(a.eps) != len(b.eps) {
			return len(a.eps) < len(b.eps)
		}
		return a.num < b.num
	})
	lo := s.shards[nums[0]]
	hi := s.shards[nums[1]]
	if len(lo.eps)+len(hi.eps) > s.m {
		return
	}
	if lo.num > hi.num {
		lo, hi = hi, lo
	}
	for id, e := range hi.eps {
		lo.eps[id] = e
	}
	delete(s.shards, hi.num)
	deletedGen[hi.num] = hi.gen + 1
	modified[hi.num] = true
	modified[lo.num] = true
}

func (s *naiveService) naiveReport(modified map[int]bool, deletedGen map[int]uint64) *SyncReport {
	nums := make([]int, 0, len(modified))
	for n := range modified {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	rep := &SyncReport{Changes: make([]ShardChange, 0, len(nums))}
	for _, n := range nums {
		if sh, ok := s.shards[n]; ok {
			sh.gen++
			rep.Changes = append(rep.Changes, ShardChange{
				ShardNum:   n,
				Generation: sh.gen,
				Endpoints:  naiveSortedEndpoints(sh),
			})
		} else {
			rep.Changes = append(rep.Changes, ShardChange{
				ShardNum:   n,
				Generation: deletedGen[n],
				Deleted:    true,
			})
		}
	}
	return rep
}

func naiveSortedEndpoints(sh *naiveShard) []Endpoint {
	out := make([]Endpoint, 0, len(sh.eps))
	for _, e := range sh.eps {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *naiveService) sync(desired []Endpoint) *SyncReport {
	want := make(map[string]Endpoint, len(desired))
	for _, e := range desired {
		want[e.ID] = e
	}
	modified := make(map[int]bool)
	deletedGen := make(map[int]uint64)

	var removed []string
	for _, sh := range s.shards {
		for id := range sh.eps {
			if _, ok := want[id]; !ok {
				removed = append(removed, id)
			}
		}
	}
	sort.Strings(removed)
	for _, id := range removed {
		sh := s.findEndpoint(id)
		delete(sh.eps, id)
		modified[sh.num] = true
	}

	var updated []string
	for id, e := range want {
		if sh := s.findEndpoint(id); sh != nil && sh.eps[id] != e {
			updated = append(updated, id)
		}
	}
	sort.Strings(updated)
	for _, id := range updated {
		sh := s.findEndpoint(id)
		sh.eps[id] = want[id]
		modified[sh.num] = true
	}

	var added []Endpoint
	for id, e := range want {
		if s.findEndpoint(id) == nil {
			added = append(added, e)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	for _, e := range added {
		sh := s.naivePlacement()
		sh.eps[e.ID] = e
		modified[sh.num] = true
	}

	s.naiveCleanup(modified, deletedGen)
	return s.naiveReport(modified, deletedGen)
}

func (s *naiveService) resize(newM int) *SyncReport {
	modified := make(map[int]bool)
	deletedGen := make(map[int]uint64)
	oldM := s.m
	s.m = newM
	if newM >= oldM {
		return s.naiveReport(modified, deletedGen)
	}

	var evicted []Endpoint
	for _, n := range s.sortedNums() {
		sh := s.shards[n]
		if len(sh.eps) <= newM {
			continue
		}
		ids := make([]string, 0, len(sh.eps))
		for id := range sh.eps {
			ids = append(ids, id)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
		ids = ids[:len(sh.eps)-newM]
		for _, id := range ids {
			evicted = append(evicted, sh.eps[id])
			delete(sh.eps, id)
			modified[sh.num] = true
		}
	}
	sort.Slice(evicted, func(i, j int) bool { return evicted[i].ID < evicted[j].ID })
	for _, e := range evicted {
		sh := s.naivePlacement()
		sh.eps[e.ID] = e
		modified[sh.num] = true
	}

	s.naiveCleanup(modified, deletedGen)
	return s.naiveReport(modified, deletedGen)
}

func (s *naiveService) query(region string) *QueryResult {
	var ready, draining []Endpoint
	for _, sh := range s.shards {
		for _, e := range sh.eps {
			if e.Ready() {
				ready = append(ready, e)
			}
			if e.Servable() && e.IsTerminating() {
				draining = append(draining, e)
			}
		}
	}
	pool := ready
	fallback := false
	if len(pool) == 0 {
		pool = draining
		fallback = true
	}
	sort.Slice(pool, func(i, j int) bool {
		sameI := pool[i].Region == region
		sameJ := pool[j].Region == region
		if sameI != sameJ {
			return sameI
		}
		return pool[i].ID < pool[j].ID
	})
	return &QueryResult{Endpoints: pool, Fallback: fallback}
}

func (s *naiveService) snapshot() []ShardInfo {
	out := make([]ShardInfo, 0, len(s.shards))
	for _, n := range s.sortedNums() {
		sh := s.shards[n]
		out = append(out, ShardInfo{
			Num:        n,
			Generation: sh.gen,
			Endpoints:  naiveSortedEndpoints(sh),
		})
	}
	return out
}

// naiveManager mirrors Manager with the same error semantics.
type naiveManager struct {
	services map[string]*naiveService
}

func newNaiveManager() *naiveManager {
	return &naiveManager{services: make(map[string]*naiveService)}
}

func (nm *naiveManager) create(name string, capacity int) error {
	if name == "" {
		return invalidArg("CreateService", name, "service name must not be empty")
	}
	if capacity <= 0 {
		return invalidArg("CreateService", name, "capacity must be positive, got %d", capacity)
	}
	if _, ok := nm.services[name]; ok {
		return alreadyExists("CreateService", name)
	}
	nm.services[name] = newNaiveService(capacity)
	return nil
}

func (nm *naiveManager) delete(name string) error {
	if name == "" {
		return invalidArg("DeleteService", name, "service name must not be empty")
	}
	if _, ok := nm.services[name]; !ok {
		return notFound("DeleteService", name)
	}
	delete(nm.services, name)
	return nil
}

func (nm *naiveManager) sync(name string, desired []Endpoint) (*SyncReport, error) {
	if err := validateDesired("Sync", name, desired); err != nil {
		return nil, err
	}
	svc, ok := nm.services[name]
	if !ok {
		return nil, notFound("Sync", name)
	}
	return svc.sync(desired), nil
}

func (nm *naiveManager) resize(name string, capacity int) (*SyncReport, error) {
	if capacity <= 0 {
		return nil, invalidArg("Resize", name, "capacity must be positive, got %d", capacity)
	}
	svc, ok := nm.services[name]
	if !ok {
		return nil, notFound("Resize", name)
	}
	return svc.resize(capacity), nil
}

func (nm *naiveManager) query(name, region string) (*QueryResult, error) {
	svc, ok := nm.services[name]
	if !ok {
		return nil, notFound("Query", name)
	}
	return svc.query(region), nil
}

// --- comparison helpers ---

func endpointsEqual(a, b []Endpoint) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func reportsEqual(a, b *SyncReport) bool {
	if len(a.Changes) != len(b.Changes) {
		return false
	}
	for i := range a.Changes {
		ca, cb := a.Changes[i], b.Changes[i]
		if ca.ShardNum != cb.ShardNum || ca.Generation != cb.Generation || ca.Deleted != cb.Deleted {
			return false
		}
		if !endpointsEqual(ca.Endpoints, cb.Endpoints) {
			return false
		}
	}
	return true
}

func snapshotsEqual(a, b []ShardInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Num != b[i].Num || a[i].Generation != b[i].Generation {
			return false
		}
		if !endpointsEqual(a[i].Endpoints, b[i].Endpoints) {
			return false
		}
	}
	return true
}

func queryEqual(a, b *QueryResult) bool {
	return a.Fallback == b.Fallback && endpointsEqual(a.Endpoints, b.Endpoints)
}

func errKind(err error) ErrorKind {
	kind, _ := KindOf(err)
	return kind
}

func describeEndpoints(eps []Endpoint) string {
	parts := make([]string, 0, len(eps))
	for _, e := range eps {
		parts = append(parts, fmt.Sprintf("%s@%s(h=%v,t=%v)", e.ID, e.Region, e.Healthy, e.Terminating))
	}
	return "[" + fmt.Sprint(parts) + "]"
}
