package whiteboard

import (
	"sort"
	"sync"
)

// 参数取值范围。
const (
	MaxNow     int64 = 1_000_000_000_000 // now 上限（含）
	MinTTL     int64 = 1
	MaxTTL     int64 = 3600
	MinMembers       = 2
	MaxMembers       = 200
)

// Side 表示 Reorder 的落点方向。
type Side int

const (
	// Below 落点紧贴 anchor 下方。
	Below Side = iota
	// Above 落点紧贴 anchor 上方。
	Above
)

// elemMeta 是每个元素的元数据。
type elemMeta struct {
	rev   uint64 // 最近一次影响该元素的全局修订号
	group string // 所属组合标识，"" 表示不属于任何组合
}

// Board 是多人实时协作白板的元素叠放与编辑锁服务。
//
// 所有公开方法可并发调用：内部以单一互斥锁串行化，
// 结果等价于按互斥锁获取顺序排列的某个串行顺序。
type Board struct {
	mu      sync.Mutex
	ol      *orderList
	elems   map[string]*elemMeta
	groups  map[string]map[string]bool
	locks   *lockTable
	rev     uint64
	lastNow int64 // 上一次被接受操作的 now；初始为 -1 表示尚无操作
}

// New 创建一块空白板，全局修订号从 0 开始。
func New() *Board {
	return &Board{
		ol:      newOrderList(),
		elems:   make(map[string]*elemMeta),
		groups:  make(map[string]map[string]bool),
		locks:   newLockTable(),
		lastNow: -1,
	}
}

// Revision 返回当前全局修订号。查询不改修订号与时钟。
func (b *Board) Revision() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rev
}

// Order 返回自底向上的完整标识序列。查询不改修订号与时钟。
func (b *Board) Order() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ol.order()
}

// Rank 返回元素自底起的名次（从 0 开始），不存在时 ok=false。O(log n)。
func (b *Board) Rank(id string) (rank int, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ol.rank(id)
}

// Between 按名次返回区间 [lo, hi]（含两端）内的元素，自底向上。
func (b *Board) Between(lo, hi int) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.ol.len()
	if lo < 0 || hi < lo || hi >= n {
		return nil, newErr(ErrInvalidParam, "between: invalid range [%d,%d] of %d elements", lo, hi, n)
	}
	out := make([]string, 0, hi-lo+1)
	for k := lo; k <= hi; k++ {
		out = append(out, b.ol.kth(k))
	}
	return out, nil
}

// ---- 内部校验与状态辅助 ----

// checkUserNow 校验公共参数：user 非空、now 在 [0, MaxNow] 内（参数非法）。
// 各操作须先完成全部参数校验，再调用 checkClock，以满足
// “参数非法 > 时钟回退”的拒绝优先级。
func (b *Board) checkUserNow(user string, now int64) *Error {
	if user == "" {
		return newErr(ErrInvalidParam, "user must be non-empty")
	}
	if now < 0 || now > MaxNow {
		return newErr(ErrInvalidParam, "now %d out of range [0,%d]", now, MaxNow)
	}
	return nil
}

// checkClock 校验时钟不回退。
func (b *Board) checkClock(now int64) *Error {
	if now < b.lastNow {
		return newErr(ErrClock, "clock rollback: now %d < last accepted now %d", now, b.lastNow)
	}
	return nil
}

// elementLockedByOther 报告元素 e 在 now 时刻是否被 user 之外的用户
// 通过未到期锁（元素自身的锁或其所属组合的锁）锁住。
// 返回持有者与剩余秒数。
func (b *Board) elementLockedByOther(e, user string, now int64) (holder string, remaining int64, locked bool) {
	if l, ok := b.locks.active(e, now); ok && l.owner != user {
		return l.owner, l.expireAt - now, true
	}
	if g := b.elems[e].group; g != "" {
		if l, ok := b.locks.active(g, now); ok && l.owner != user {
			return l.owner, l.expireAt - now, true
		}
	}
	return "", 0, false
}

// checkMovingSetLocked 按自底向上的次序检查移动集合，
// 返回第一个被他人锁住的元素对应的错误；无冲突返回 nil。
func (b *Board) checkMovingSetLocked(moving []string, user string, now int64) *Error {
	for _, e := range moving {
		if holder, remaining, locked := b.elementLockedByOther(e, user, now); locked {
			return lockedErr(holder, remaining, "element %q is locked by another user", e)
		}
	}
	return nil
}

// membersByRank 返回组合成员按当前名次自底向上排序的序列。
func (b *Board) membersByRank(gid string) []string {
	set := b.groups[gid]
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	b.ol.sortByRank(ids)
	return ids
}

// ---- 修改类操作 ----

// Add 把新元素放到最顶。标识在元素与组合共用的命名空间中必须唯一。
func (b *Board) Add(user, id string, now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id == "" {
		return newErr(ErrInvalidParam, "id must be non-empty")
	}
	if b.ol.has(id) || b.groups[id] != nil {
		return newErr(ErrInvalidParam, "id %q already exists", id)
	}
	if err := b.checkUserNow(user, now); err != nil {
		return err
	}
	if err := b.checkClock(now); err != nil {
		return err
	}

	b.ol.addTop(id)
	b.rev++
	b.elems[id] = &elemMeta{rev: b.rev}
	b.lastNow = now
	return nil
}

// Group 建立组合：成员数为 2 到 200，成员须已存在且尚未属于任何组合。
// 组合的建立影响全部成员（最近影响修订号更新）。
func (b *Board) Group(user, groupID string, ids []string, now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if groupID == "" {
		return newErr(ErrInvalidParam, "group id must be non-empty")
	}
	if len(ids) < MinMembers || len(ids) > MaxMembers {
		return newErr(ErrInvalidParam, "group needs %d..%d members, got %d", MinMembers, MaxMembers, len(ids))
	}
	if b.ol.has(groupID) || b.groups[groupID] != nil {
		return newErr(ErrInvalidParam, "id %q already exists", groupID)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return newErr(ErrInvalidParam, "member id must be non-empty")
		}
		if id == groupID {
			return newErr(ErrInvalidParam, "group %q cannot contain itself", groupID)
		}
		if seen[id] {
			return newErr(ErrInvalidParam, "duplicate member %q", id)
		}
		seen[id] = true
	}
	if err := b.checkUserNow(user, now); err != nil {
		return err
	}
	if err := b.checkClock(now); err != nil {
		return err
	}

	for _, id := range ids {
		if !b.ol.has(id) {
			return newErr(ErrNotFound, "member %q does not exist", id)
		}
	}
	for _, id := range ids {
		if b.elems[id].group != "" {
			return newErr(ErrInvalidTarget, "member %q already belongs to group %q", id, b.elems[id].group)
		}
	}
	if err := b.checkMovingSetLocked(ids, user, now); err != nil {
		return err
	}

	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
		b.elems[id].group = groupID
	}
	b.groups[groupID] = set
	b.rev++
	for _, id := range ids {
		b.elems[id].rev = b.rev
	}
	b.lastNow = now
	return nil
}

// Ungroup 解散组合，不改变任何元素的次序。解散影响全部成员。
// 组合上的锁随组合一并清除。
func (b *Board) Ungroup(user, groupID string, now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if groupID == "" {
		return newErr(ErrInvalidParam, "group id must be non-empty")
	}
	if err := b.checkUserNow(user, now); err != nil {
		return err
	}
	if err := b.checkClock(now); err != nil {
		return err
	}
	if b.groups[groupID] == nil {
		if b.ol.has(groupID) {
			return newErr(ErrInvalidTarget, "%q is an element, not a group", groupID)
		}
		return newErr(ErrNotFound, "group %q does not exist", groupID)
	}
	members := b.membersByRank(groupID)
	if err := b.checkMovingSetLocked(members, user, now); err != nil {
		return err
	}

	delete(b.groups, groupID)
	b.locks.del(groupID)
	b.rev++
	for _, id := range members {
		b.elems[id].group = ""
		b.elems[id].rev = b.rev
	}
	b.lastNow = now
	return nil
}

// Remove 删除一个元素或组合。删除组合影响其全部成员。
// 被删除对象上的锁一并清除；删除组合成员会使其退出组合。
func (b *Board) Remove(user, target string, now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if target == "" {
		return newErr(ErrInvalidParam, "target must be non-empty")
	}
	if err := b.checkUserNow(user, now); err != nil {
		return err
	}
	if err := b.checkClock(now); err != nil {
		return err
	}
	isGroup := b.groups[target] != nil
	if !isGroup && !b.ol.has(target) {
		return newErr(ErrNotFound, "target %q does not exist", target)
	}

	var involved []string
	if isGroup {
		involved = b.membersByRank(target)
	} else {
		involved = []string{target}
	}
	if err := b.checkMovingSetLocked(involved, user, now); err != nil {
		return err
	}

	b.rev++
	if isGroup {
		for _, id := range involved {
			b.elems[id].rev = b.rev // 记录影响后再移除
			b.ol.remove(id)
			delete(b.elems, id)
			b.locks.del(id)
		}
		delete(b.groups, target)
		b.locks.del(target)
	} else {
		m := b.elems[target]
		m.rev = b.rev
		if m.group != "" {
			delete(b.groups[m.group], target)
		}
		b.ol.remove(target)
		delete(b.elems, target)
		b.locks.del(target)
	}
	b.lastNow = now
	return nil
}

// Reorder 把 target（元素或组合）移动到 anchor 的上方或下方，紧贴 anchor。
// target 为组合时全体成员作为连续一段移动，段内相对次序保持不变。
// 直接对属于某组合的元素单独 Reorder 须拒绝。
// 移动集合中任一元素的最近影响修订号大于 baseRev 即视为版本冲突。
func (b *Board) Reorder(user, target, anchor string, side Side, baseRev uint64, now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if target == "" || anchor == "" {
		return newErr(ErrInvalidParam, "target and anchor must be non-empty")
	}
	if side != Below && side != Above {
		return newErr(ErrInvalidParam, "invalid side %d", side)
	}
	if err := b.checkUserNow(user, now); err != nil {
		return err
	}
	if err := b.checkClock(now); err != nil {
		return err
	}

	targetIsGroup := b.groups[target] != nil
	if !targetIsGroup && !b.ol.has(target) {
		return newErr(ErrNotFound, "target %q does not exist", target)
	}
	if !b.ol.has(anchor) {
		if b.groups[anchor] != nil {
			return newErr(ErrInvalidTarget, "anchor %q is a group, must be an element", anchor)
		}
		return newErr(ErrNotFound, "anchor %q does not exist", anchor)
	}

	var moving []string
	if targetIsGroup {
		moving = b.membersByRank(target)
	} else {
		if g := b.elems[target].group; g != "" {
			return newErr(ErrInvalidTarget, "element %q belongs to group %q, reorder the group instead", target, g)
		}
		moving = []string{target}
	}
	for _, id := range moving {
		if id == anchor {
			return newErr(ErrInvalidTarget, "anchor %q is inside the moving set", anchor)
		}
	}

	if err := b.checkMovingSetLocked(moving, user, now); err != nil {
		return err
	}
	for _, id := range moving {
		if b.elems[id].rev > baseRev {
			return newErr(ErrConflict, "element %q affected by rev %d > baseRev %d", id, b.elems[id].rev, baseRev)
		}
	}

	b.ol.moveSegment(moving, anchor, side == Above)
	b.rev++
	for _, id := range moving {
		b.elems[id].rev = b.rev
	}
	b.lastNow = now
	return nil
}

// ---- 锁操作 ----

// Lock 对元素或组合加软锁，锁在 now+ttl 时刻到期（恰等于到期时刻视为已到期）。
// 同一用户对自己持有的锁再次加锁视为续期；他人持有未到期锁（含组合锁
// 覆盖成员、成员锁牵连组合）时加锁被拒。不改修订号。
func (b *Board) Lock(user, id string, ttl int64, now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id == "" {
		return newErr(ErrInvalidParam, "id must be non-empty")
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return newErr(ErrInvalidParam, "ttl %d out of range [%d,%d]", ttl, MinTTL, MaxTTL)
	}
	if err := b.checkUserNow(user, now); err != nil {
		return err
	}
	if err := b.checkClock(now); err != nil {
		return err
	}

	isGroup := b.groups[id] != nil
	if !isGroup && !b.ol.has(id) {
		return newErr(ErrNotFound, "lock target %q does not exist", id)
	}

	// 相关对象：元素查自身与所属组合；组合查自身与全部成员（按标识序，保证确定性）。
	related := []string{id}
	if isGroup {
		members := make([]string, 0, len(b.groups[id]))
		for m := range b.groups[id] {
			members = append(members, m)
		}
		sort.Strings(members)
		related = append(related, members...)
	} else if g := b.elems[id].group; g != "" {
		related = append(related, g)
	}
	for _, rid := range related {
		if l, ok := b.locks.active(rid, now); ok && l.owner != user {
			return lockedErr(l.owner, l.expireAt-now, "cannot lock %q: %q is locked by another user", id, rid)
		}
	}

	b.locks.set(id, user, now+ttl)
	b.lastNow = now
	return nil
}

// Unlock 解锁，仅持有者可做。不改修订号。
func (b *Board) Unlock(user, id string, now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id == "" {
		return newErr(ErrInvalidParam, "id must be non-empty")
	}
	if err := b.checkUserNow(user, now); err != nil {
		return err
	}
	if err := b.checkClock(now); err != nil {
		return err
	}
	if !b.ol.has(id) && b.groups[id] == nil {
		return newErr(ErrNotFound, "unlock target %q does not exist", id)
	}
	l, ok := b.locks.active(id, now)
	if !ok {
		return newErr(ErrInvalidTarget, "no active lock on %q", id)
	}
	if l.owner != user {
		return lockedErr(l.owner, l.expireAt-now, "cannot unlock %q held by another user", id)
	}

	b.locks.del(id)
	b.lastNow = now
	return nil
}
