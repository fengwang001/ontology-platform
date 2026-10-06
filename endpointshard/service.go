package endpointshard

import (
	"math/rand"
	"sort"
	"sync"
)

// Service 是单个服务的分片维护器。
//
// 不变式（每次同步/调整结束时成立）：
//   - 每个分片端点数在 [1, M] 之间（不存在空分片、不存在超容分片）；
//   - 端点标识全局唯一，locate 与分片成员关系互为逆映射；
//   - placement 恰好包含所有未满（端点数 < M）的分片；
//   - byCount 恰好包含所有分片；
//   - ready/fallback 恰好包含满足派生条件的端点标识。
//
// 复杂度：同步开销为 O(|期望集| + 受影响端点数·log n + 受影响分片数·log S)，
// 不扫描未受影响的分片；查询开销与结果大小成正比。
type Service struct {
	mu sync.RWMutex
	m  int

	endpoints map[string]Endpoint // 标识 -> 端点数据（唯一存储）
	locate    map[string]int      // 标识 -> 分片编号
	shards    map[int]*shard

	lastShardID int // 已分配的最大分片编号，单调递增、永不复用

	ready     *orderedSet[string]   // 就绪端点标识（健康且非终止中）
	fallback  *orderedSet[string]   // 可服务且终止中的端点标识
	placement *orderedSet[placeKey] // 未满分片的落位索引
	byCount   *orderedSet[countKey] // 全部分片的整理索引

	stats Stats
}

func newService(m int, rng *rand.Rand) *Service {
	return &Service{
		m:         m,
		endpoints: make(map[string]Endpoint),
		locate:    make(map[string]int),
		shards:    make(map[int]*shard),
		ready:     newOrderedSet(lessString, rng),
		fallback:  newOrderedSet(lessString, rng),
		placement: newOrderedSet(lessPlaceKey, rng),
		byCount:   newOrderedSet(lessCountKey, rng),
	}
}

// opCtx 承载一次同步/调整操作的上下文：报告构建器与本次变空的分片。
type opCtx struct {
	svc     *Service
	rb      *reportBuilder
	emptied []int
}

// Sync 把分片调整到与期望端点全集一致，返回精确的变更报告。
// 调用方（Manager）已完成参数校验。
func (s *Service) Sync(desired []Endpoint) ChangeReport {
	s.mu.Lock()
	defer s.mu.Unlock()

	desiredByID := make(map[string]Endpoint, len(desired))
	for _, ep := range desired {
		desiredByID[ep.ID] = ep
	}

	ctx := &opCtx{svc: s, rb: newReportBuilder(s)}
	changed := false

	// 已不在期望集合中的端点从其分片中移除。
	// 注意：|当前集| <= |期望集| + |被移除数|，此循环不引入
	// 超出输入规模与受影响数量的开销。
	for id, cur := range s.endpoints {
		if _, ok := desiredByID[id]; !ok {
			ctx.removeEndpoint(id, cur)
			changed = true
		}
	}

	// 仍在期望集合中的端点留在原分片，仅更新内容；收集新增端点。
	var added []Endpoint
	for _, ep := range desired {
		cur, ok := s.endpoints[ep.ID]
		if !ok {
			added = append(added, ep)
			continue
		}
		if cur != ep {
			ctx.updateEndpoint(cur, ep)
			changed = true
		}
	}
	if len(added) > 0 {
		changed = true
	}

	// 期望集与当前完全一致：报告为空，不改变任何代次，也不触发整理。
	if !changed {
		return ChangeReport{}
	}

	// 新增端点按标识字典序依次落位。
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	for _, ep := range added {
		ctx.placeNew(ep)
	}

	ctx.cleanup()
	return ctx.rb.build()
}

// Query 按消费者所在区域查询。
func (s *Service) Query(region string) QueryResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	src := s.ready
	fallback := s.ready.Len() == 0
	if fallback {
		src = s.fallback
	}
	res := QueryResult{Fallback: fallback}
	if src.Len() == 0 {
		return res
	}
	var same, other []Endpoint
	src.Ascend(&s.stats.QueryVisits, func(id string) bool {
		ep := s.endpoints[id]
		if ep.Region == region {
			same = append(same, ep)
		} else {
			other = append(other, ep)
		}
		return true
	})
	res.Endpoints = append(same, other...)
	return res
}

// Resize 调整分片容量 M。
//
// 变大：不触发任何搬移与整理，只影响此后的落位与合并判断。
// 变小：超容分片中标识字典序最大的端点被移出，全部被移出的端点
// 按标识字典序依新增端点规则重新落位，直到没有超容分片；
// 移出与重新落位属于同一次原子调整，随后按同样的规则整理一次。
func (s *Service) Resize(newM int) ChangeReport {
	s.mu.Lock()
	defer s.mu.Unlock()

	if newM == s.m {
		return ChangeReport{}
	}
	ctx := &opCtx{svc: s, rb: newReportBuilder(s)}

	if newM > s.m {
		oldM := s.m
		s.m = newM
		// 原本恰好满（端点数 == oldM）的分片变为未满，补入落位索引。
		var ids []int
		s.byCount.AscendFrom(countKey{count: oldM}, &s.stats.IndexVisits, func(k countKey) bool {
			if k.count != oldM {
				return false
			}
			ids = append(ids, k.id)
			return true
		})
		for _, id := range ids {
			sh := s.shards[id]
			s.placement.Insert(placeKey{negCount: -sh.count(), id: id}, &s.stats.IndexVisits)
		}
		return ctx.rb.build()
	}

	s.m = newM

	// 找出全部超容分片（端点数 > newM），按编号升序处理。
	var overIDs []int
	s.byCount.AscendFrom(countKey{count: newM + 1}, &s.stats.IndexVisits, func(k countKey) bool {
		overIDs = append(overIDs, k.id)
		return true
	})
	sort.Ints(overIDs)

	// 先完成全部移出：每个超容分片移出标识字典序最大的端点，直到不再超容。
	var evicted []Endpoint
	for _, id := range overIDs {
		sh := s.shards[id]
		members := s.sortedMemberIDs(sh)
		for _, epID := range members[newM:] {
			ep := s.endpoints[epID]
			evicted = append(evicted, ep)
			ctx.removeEndpoint(epID, ep)
		}
	}

	// 修正落位索引：端点数恰好等于新 M 的分片现在是满分片。
	var fullIDs []int
	s.byCount.AscendFrom(countKey{count: newM}, &s.stats.IndexVisits, func(k countKey) bool {
		if k.count != newM {
			return false
		}
		fullIDs = append(fullIDs, k.id)
		return true
	})
	for _, id := range fullIDs {
		s.placement.Remove(placeKey{negCount: -newM, id: id}, &s.stats.IndexVisits)
	}

	// 被移出的端点按标识字典序依新增端点规则重新落位。
	sort.Slice(evicted, func(i, j int) bool { return evicted[i].ID < evicted[j].ID })
	for _, ep := range evicted {
		ctx.placeNew(ep)
	}

	ctx.cleanup()
	return ctx.rb.build()
}

// Describe 返回服务的只读快照。
func (s *Service) Describe() ServiceView {
	s.mu.RLock()
	defer s.mu.RUnlock()

	view := ServiceView{M: s.m, LastShardID: s.lastShardID}
	ids := make([]int, 0, len(s.shards))
	for id := range s.shards {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		sh := s.shards[id]
		view.Shards = append(view.Shards, ShardView{
			ID:         id,
			Generation: sh.gen,
			Endpoints:  s.sortedMembers(sh),
		})
	}
	return view
}

// ResetStats 清零操作计数器（用于性能可验证性测试）。
func (s *Service) ResetStats() { s.stats.reset() }

// Stats 返回操作计数器快照。
func (s *Service) Stats() StatsSnapshot { return s.stats.snapshot() }

// ---- 内部变更原语（调用方须持有写锁） ----

// removeEndpoint 把端点从其分片中移除并维护全部索引。
func (ctx *opCtx) removeEndpoint(id string, cur Endpoint) {
	s := ctx.svc
	sh := s.shards[s.locate[id]]
	s.removeFromIndexes(cur)
	delete(s.endpoints, id)
	delete(s.locate, id)
	s.unlinkShard(sh)
	delete(sh.members, id)
	s.linkShard(sh)
	ctx.rb.markUpdated(sh)
	if sh.count() == 0 {
		ctx.emptied = append(ctx.emptied, sh.id)
	}
}

// updateEndpoint 只更新端点内容，不换分片。
func (ctx *opCtx) updateEndpoint(old, ep Endpoint) {
	s := ctx.svc
	s.removeFromIndexes(old)
	s.endpoints[ep.ID] = ep
	s.addToIndexes(ep)
	ctx.rb.markUpdated(s.shards[s.locate[ep.ID]])
}

// placeNew 按新增端点规则落位：端点数最多但未满的分片（并列取编号小者），
// 没有未满的分片则新建分片。
func (ctx *opCtx) placeNew(ep Endpoint) {
	s := ctx.svc
	var sh *shard
	if k, ok := s.placement.Min(&s.stats.IndexVisits); ok {
		sh = s.shards[k.id]
		ctx.rb.markUpdated(sh)
	} else {
		s.lastShardID++
		sh = newShard(s.lastShardID)
		s.shards[sh.id] = sh
		ctx.rb.markCreated(sh)
	}
	s.unlinkShard(sh)
	sh.members[ep.ID] = struct{}{}
	s.linkShard(sh)
	s.endpoints[ep.ID] = ep
	s.locate[ep.ID] = sh.id
	s.addToIndexes(ep)
}

// cleanup 同步/调整结束前的整理：先删除所有空分片，
// 然后至多合并一次（端点数最少的两个分片之和不超过 M 时，
// 编号较大者并入编号较小者）。
func (ctx *opCtx) cleanup() {
	s := ctx.svc
	for _, id := range ctx.emptied {
		sh, ok := s.shards[id]
		if !ok || sh.count() != 0 {
			continue
		}
		s.unlinkShard(sh)
		delete(s.shards, id)
		ctx.rb.markDeleted(sh)
	}

	if len(s.shards) < 2 {
		return
	}
	two := s.byCount.FirstTwo(&s.stats.IndexVisits)
	if len(two) < 2 {
		return
	}
	a, b := s.shards[two[0].id], s.shards[two[1].id]
	if a.count()+b.count() > s.m {
		return
	}
	src, dst := a, b
	if src.id < dst.id {
		src, dst = dst, src
	}
	s.unlinkShard(src)
	s.unlinkShard(dst)
	for id := range src.members {
		dst.members[id] = struct{}{}
		s.locate[id] = dst.id
	}
	s.linkShard(dst)
	delete(s.shards, src.id)
	ctx.rb.markUpdated(dst)
	ctx.rb.markDeleted(src)
}

// unlinkShard 把分片按键的当前值从索引中摘除（幂等）。
func (s *Service) unlinkShard(sh *shard) {
	c := sh.count()
	s.byCount.Remove(countKey{count: c, id: sh.id}, &s.stats.IndexVisits)
	s.placement.Remove(placeKey{negCount: -c, id: sh.id}, &s.stats.IndexVisits)
}

// linkShard 把分片按键的当前值装回索引（幂等）；仅未满分片进入落位索引。
func (s *Service) linkShard(sh *shard) {
	c := sh.count()
	s.byCount.Insert(countKey{count: c, id: sh.id}, &s.stats.IndexVisits)
	if c < s.m {
		s.placement.Insert(placeKey{negCount: -c, id: sh.id}, &s.stats.IndexVisits)
	}
}

func (s *Service) addToIndexes(ep Endpoint) {
	if ep.Ready() {
		s.ready.Insert(ep.ID, &s.stats.IndexVisits)
	} else if ep.Serveable() && ep.IsTerminating() {
		s.fallback.Insert(ep.ID, &s.stats.IndexVisits)
	}
}

func (s *Service) removeFromIndexes(ep Endpoint) {
	if ep.Ready() {
		s.ready.Remove(ep.ID, &s.stats.IndexVisits)
	} else if ep.Serveable() && ep.IsTerminating() {
		s.fallback.Remove(ep.ID, &s.stats.IndexVisits)
	}
}

func (s *Service) sortedMemberIDs(sh *shard) []string {
	ids := make([]string, 0, len(sh.members))
	for id := range sh.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) sortedMembers(sh *shard) []Endpoint {
	ids := s.sortedMemberIDs(sh)
	out := make([]Endpoint, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.endpoints[id])
	}
	return out
}

// reportBuilder 在一次操作内累积被修改的分片。
// 分片在报告中出现时其修改代次恰好递增一次（含新建与被删除）。
type reportBuilder struct {
	svc     *Service
	entries map[int]*ShardChange
}

func newReportBuilder(svc *Service) *reportBuilder {
	return &reportBuilder{svc: svc, entries: make(map[int]*ShardChange)}
}

func (rb *reportBuilder) entry(sh *shard) *ShardChange {
	if e, ok := rb.entries[sh.id]; ok {
		return e
	}
	sh.gen++
	e := &ShardChange{ShardID: sh.id, Generation: sh.gen}
	rb.entries[sh.id] = e
	return e
}

func (rb *reportBuilder) markCreated(sh *shard) {
	rb.entry(sh).Kind = ChangeCreated
}

func (rb *reportBuilder) markUpdated(sh *shard) {
	e := rb.entry(sh)
	if e.Kind != ChangeCreated {
		e.Kind = ChangeUpdated
	}
}

func (rb *reportBuilder) markDeleted(sh *shard) {
	e := rb.entry(sh)
	e.Kind = ChangeDeleted
	e.Endpoints = nil
}

func (rb *reportBuilder) build() ChangeReport {
	if len(rb.entries) == 0 {
		return ChangeReport{}
	}
	ids := make([]int, 0, len(rb.entries))
	for id := range rb.entries {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	rep := ChangeReport{Shards: make([]ShardChange, 0, len(ids))}
	for _, id := range ids {
		e := rb.entries[id]
		if e.Kind != ChangeDeleted {
			e.Endpoints = rb.svc.sortedMembers(rb.svc.shards[id])
		}
		rep.Shards = append(rep.Shards, *e)
	}
	return rep
}
