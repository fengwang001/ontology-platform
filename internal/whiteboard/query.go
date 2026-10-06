package whiteboard

import "slices"

// Order 返回自底向上的完整标识序列（副本）。查询不改修订号、次序、锁与时钟。
func (b *Board) Order() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.order.inorder()
}

// Rank 返回元素自底起的名次（最底为 1），开销不随元素总数线性增长。
func (b *Board) Rank(id string) (int, error) {
	if !validID(id) {
		return 0, reject(KindInvalidArgument, "Rank: empty id")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	x, ok := b.nodes[id]
	if !ok {
		if _, isGroup := b.groups[id]; isGroup {
			return 0, reject(KindIllegal, "Rank: %q is a group, not an element", id)
		}
		return 0, reject(KindNotFound, "Rank: element %q does not exist", id)
	}
	return b.order.rankOf(x), nil
}

// Between 按 1 基名次返回闭区间 [lo,hi] 内元素，自底向上。
func (b *Board) Between(lo, hi int) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := b.order.size()
	if lo < 1 || hi < lo || hi > total {
		return nil, reject(KindInvalidArgument, "Between: require 1<=lo<=hi<=%d, got [%d,%d]", total, lo, hi)
	}
	return b.order.between(lo, hi), nil
}

// Rev 返回当前全局修订号。
func (b *Board) Rev() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rev
}

// Snapshot 返回当前完整状态（供测试/对照）。
func (b *Board) Snapshot(now int64) Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := Snapshot{
		Order:  b.order.inorder(),
		Rev:    b.rev,
		LastTs: b.lastTs,
		Elems:  map[string]ElemInfo{},
		Groups: map[string][]string{},
		Locks:  map[string]LockState{},
	}
	for id, e := range b.elems {
		snap.Elems[id] = ElemInfo{Group: e.group, LastRev: e.lastRev}
	}
	for gid, members := range b.groups {
		list := make([]string, 0, len(members))
		for m := range members {
			list = append(list, m)
		}
		slices.Sort(list)
		snap.Groups[gid] = list
	}
	for id, l := range b.locks {
		if l.expireAt <= now {
			continue
		}
		snap.Locks[id] = LockState{TargetID: id, Holder: l.holder, ExpireAt: l.expireAt}
	}
	return snap
}
