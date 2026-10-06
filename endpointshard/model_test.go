package endpointshard

import "sort"

// naiveModel 是独立实现的朴素模型：不做任何索引优化，
// 每次操作都按规则字面意思全量扫描，用于与优化实现逐步对照。
type naiveModel struct {
	services map[string]*naiveService
}

type naiveService struct {
	m      int
	lastID int
	shards map[int]*naiveShard
	loc    map[string]int
}

type naiveShard struct {
	gen     uint64
	members map[string]Endpoint
}

func newNaiveModel() *naiveModel { return &naiveModel{services: map[string]*naiveService{}} }

func (n *naiveModel) create(name string, m int) {
	n.services[name] = &naiveService{m: m, shards: map[int]*naiveShard{}, loc: map[string]int{}}
}

func (n *naiveModel) delete(name string) { delete(n.services, name) }

// naiveOp 在一次操作内累积报告，规则与优化实现一致：
// 分片在报告里出现时代次恰好递增一次（含新建与被删除）。
type naiveOp struct {
	svc     *naiveService
	kinds   map[int]ChangeKind
	gens    map[int]uint64
	emptied []int
}

func newNaiveOp(svc *naiveService) *naiveOp {
	return &naiveOp{svc: svc, kinds: map[int]ChangeKind{}, gens: map[int]uint64{}}
}

func (op *naiveOp) mark(shardID int, kind ChangeKind) {
	if _, ok := op.kinds[shardID]; !ok {
		sh := op.svc.shards[shardID]
		sh.gen++
		op.kinds[shardID] = kind
		op.gens[shardID] = sh.gen
		return
	}
	if kind == ChangeDeleted {
		op.kinds[shardID] = ChangeDeleted
	} else if kind == ChangeUpdated && op.kinds[shardID] != ChangeCreated {
		op.kinds[shardID] = ChangeUpdated
	}
}

func (op *naiveOp) build() ChangeReport {
	if len(op.kinds) == 0 {
		return ChangeReport{}
	}
	ids := make([]int, 0, len(op.kinds))
	for id := range op.kinds {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	rep := ChangeReport{}
	for _, id := range ids {
		kind := op.kinds[id]
		sc := ShardChange{ShardID: id, Kind: kind, Generation: op.gens[id]}
		if kind != ChangeDeleted {
			sc.Endpoints = op.svc.sortedMembers(id)
		}
		rep.Shards = append(rep.Shards, sc)
	}
	return rep
}

func (s *naiveService) sortedMembers(shardID int) []Endpoint {
	sh := s.shards[shardID]
	ids := make([]string, 0, len(sh.members))
	for id := range sh.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Endpoint, 0, len(ids))
	for _, id := range ids {
		out = append(out, sh.members[id])
	}
	return out
}

func (s *naiveService) sortedShardIDs() []int {
	ids := make([]int, 0, len(s.shards))
	for id := range s.shards {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// remove 把端点从其分片中移除。
func (op *naiveOp) remove(id string) {
	s := op.svc
	shID := s.loc[id]
	sh := s.shards[shID]
	delete(sh.members, id)
	delete(s.loc, id)
	op.mark(shID, ChangeUpdated)
	if len(sh.members) == 0 {
		op.emptied = append(op.emptied, shID)
	}
}

// placeNew 按新增端点规则落位：全量扫描找端点数最多但未满的分片
// （并列取编号小者），没有未满的分片则新建分片。
func (op *naiveOp) placeNew(ep Endpoint) {
	s := op.svc
	best := -1
	for _, id := range s.sortedShardIDs() {
		c := len(s.shards[id].members)
		if c >= s.m {
			continue
		}
		if best == -1 || c > len(s.shards[best].members) {
			best = id
		}
	}
	if best == -1 {
		s.lastID++
		best = s.lastID
		s.shards[best] = &naiveShard{members: map[string]Endpoint{}}
		op.mark(best, ChangeCreated)
	} else {
		op.mark(best, ChangeUpdated)
	}
	s.shards[best].members[ep.ID] = ep
	s.loc[ep.ID] = best
}

// cleanup 整理：先删除所有空分片，然后至多合并一次。
func (op *naiveOp) cleanup() {
	s := op.svc
	for _, id := range op.emptied {
		sh, ok := s.shards[id]
		if !ok || len(sh.members) != 0 {
			continue
		}
		op.mark(id, ChangeDeleted)
		delete(s.shards, id)
	}
	if len(s.shards) < 2 {
		return
	}
	ids := s.sortedShardIDs()
	sort.Slice(ids, func(i, j int) bool {
		ci, cj := len(s.shards[ids[i]].members), len(s.shards[ids[j]].members)
		if ci != cj {
			return ci < cj
		}
		return ids[i] < ids[j]
	})
	a, b := ids[0], ids[1]
	if len(s.shards[a].members)+len(s.shards[b].members) > s.m {
		return
	}
	src, dst := a, b
	if src < dst {
		src, dst = dst, src
	}
	for id, ep := range s.shards[src].members {
		s.shards[dst].members[id] = ep
		s.loc[id] = dst
	}
	op.mark(dst, ChangeUpdated)
	op.mark(src, ChangeDeleted)
	delete(s.shards, src)
}

// sync 朴素同步：字面执行全部规则。
func (n *naiveModel) sync(name string, desired []Endpoint) ChangeReport {
	s := n.services[name]
	op := newNaiveOp(s)
	desiredByID := make(map[string]Endpoint, len(desired))
	for _, ep := range desired {
		desiredByID[ep.ID] = ep
	}
	changed := false
	var removed []string
	for id := range s.loc {
		if _, ok := desiredByID[id]; !ok {
			removed = append(removed, id)
		}
	}
	sort.Strings(removed)
	for _, id := range removed {
		op.remove(id)
		changed = true
	}
	var added []Endpoint
	for _, ep := range desired {
		shID, ok := s.loc[ep.ID]
		if !ok {
			added = append(added, ep)
			continue
		}
		if s.shards[shID].members[ep.ID] != ep {
			s.shards[shID].members[ep.ID] = ep
			op.mark(shID, ChangeUpdated)
			changed = true
		}
	}
	if len(added) > 0 {
		changed = true
	}
	if !changed {
		return ChangeReport{}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	for _, ep := range added {
		op.placeNew(ep)
	}
	op.cleanup()
	return op.build()
}

// resize 朴素调整：变大不搬移不整理；变小先全部移出再统一落位，整理一次。
func (n *naiveModel) resize(name string, newM int) ChangeReport {
	s := n.services[name]
	if newM == s.m {
		return ChangeReport{}
	}
	op := newNaiveOp(s)
	if newM > s.m {
		s.m = newM
		return op.build()
	}
	s.m = newM
	var evicted []Endpoint
	for _, id := range s.sortedShardIDs() {
		sh := s.shards[id]
		if len(sh.members) <= newM {
			continue
		}
		members := s.sortedMembers(id)
		for _, ep := range members[newM:] {
			evicted = append(evicted, ep)
			op.remove(ep.ID)
		}
	}
	sort.Slice(evicted, func(i, j int) bool { return evicted[i].ID < evicted[j].ID })
	for _, ep := range evicted {
		op.placeNew(ep)
	}
	op.cleanup()
	return op.build()
}

// query 朴素查询：全量扫描计算就绪/回退集合。
func (n *naiveModel) query(name, region string) QueryResult {
	s := n.services[name]
	var ready, fallback []Endpoint
	for _, id := range s.sortedShardIDs() {
		for _, ep := range s.shards[id].members {
			if ep.Ready() {
				ready = append(ready, ep)
			} else if ep.Serveable() && ep.IsTerminating() {
				fallback = append(fallback, ep)
			}
		}
	}
	src := ready
	fellBack := len(ready) == 0
	if fellBack {
		src = fallback
	}
	sort.Slice(src, func(i, j int) bool { return src[i].ID < src[j].ID })
	res := QueryResult{Fallback: fellBack}
	var same, other []Endpoint
	for _, ep := range src {
		if ep.Region == region {
			same = append(same, ep)
		} else {
			other = append(other, ep)
		}
	}
	res.Endpoints = append(same, other...)
	return res
}

// describe 朴素快照。
func (n *naiveModel) describe(name string) ServiceView {
	s := n.services[name]
	view := ServiceView{M: s.m, LastShardID: s.lastID}
	for _, id := range s.sortedShardIDs() {
		view.Shards = append(view.Shards, ShardView{
			ID:         id,
			Generation: s.shards[id].gen,
			Endpoints:  s.sortedMembers(id),
		})
	}
	return view
}
