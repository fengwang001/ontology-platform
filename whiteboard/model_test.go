package whiteboard

// 本文件是用于随机对照测试的独立朴素模型：
// 以切片维护全序、以线性扫描完成全部校验，刻意与 Board 的实现完全独立。
// 语义与 Board 的公开契约保持一致（见 DESIGN.md）。

import "sort"

type mLock struct {
	owner    string
	expireAt int64
}

type model struct {
	order     []string
	elemRev   map[string]uint64
	elemGroup map[string]string
	groups    map[string]map[string]bool
	locks     map[string]mLock
	rev       uint64
	lastNow   int64
}

func newModel() *model {
	return &model{
		elemRev:   make(map[string]uint64),
		elemGroup: make(map[string]string),
		groups:    make(map[string]map[string]bool),
		locks:     make(map[string]mLock),
		lastNow:   -1,
	}
}

func (m *model) has(id string) bool {
	_, ok := m.elemRev[id]
	return ok
}

func (m *model) rankOf(id string) int {
	for i, x := range m.order {
		if x == id {
			return i
		}
	}
	return -1
}

func (m *model) checkUserNow(user string, now int64) *Error {
	if user == "" {
		return newErr(ErrInvalidParam, "user must be non-empty")
	}
	if now < 0 || now > MaxNow {
		return newErr(ErrInvalidParam, "now %d out of range [0,%d]", now, MaxNow)
	}
	return nil
}

func (m *model) checkClock(now int64) *Error {
	if now < m.lastNow {
		return newErr(ErrClock, "clock rollback: now %d < last accepted now %d", now, m.lastNow)
	}
	return nil
}

func (m *model) activeLock(id string, now int64) (mLock, bool) {
	l, ok := m.locks[id]
	if !ok || l.expireAt <= now {
		return mLock{}, false
	}
	return l, true
}

func (m *model) elementLockedByOther(e, user string, now int64) (string, int64, bool) {
	if l, ok := m.activeLock(e, now); ok && l.owner != user {
		return l.owner, l.expireAt - now, true
	}
	if g := m.elemGroup[e]; g != "" {
		if l, ok := m.activeLock(g, now); ok && l.owner != user {
			return l.owner, l.expireAt - now, true
		}
	}
	return "", 0, false
}

func (m *model) checkMovingSetLocked(moving []string, user string, now int64) *Error {
	for _, e := range moving {
		if h, r, locked := m.elementLockedByOther(e, user, now); locked {
			return lockedErr(h, r, "element %q is locked by another user", e)
		}
	}
	return nil
}

// membersByRank 按名次自底向上排序（名次即切片下标）。
func (m *model) membersByRank(gid string) []string {
	var ids []string
	for _, id := range m.order {
		if m.groups[gid][id] {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *model) add(user, id string, now int64) error {
	if id == "" {
		return newErr(ErrInvalidParam, "id must be non-empty")
	}
	if m.has(id) || m.groups[id] != nil {
		return newErr(ErrInvalidParam, "id %q already exists", id)
	}
	if err := m.checkUserNow(user, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.order = append(m.order, id)
	m.rev++
	m.elemRev[id] = m.rev
	m.elemGroup[id] = ""
	m.lastNow = now
	return nil
}

func (m *model) group(user, gid string, ids []string, now int64) error {
	if gid == "" {
		return newErr(ErrInvalidParam, "group id must be non-empty")
	}
	if len(ids) < MinMembers || len(ids) > MaxMembers {
		return newErr(ErrInvalidParam, "group needs %d..%d members, got %d", MinMembers, MaxMembers, len(ids))
	}
	if m.has(gid) || m.groups[gid] != nil {
		return newErr(ErrInvalidParam, "id %q already exists", gid)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return newErr(ErrInvalidParam, "member id must be non-empty")
		}
		if id == gid {
			return newErr(ErrInvalidParam, "group %q cannot contain itself", gid)
		}
		if seen[id] {
			return newErr(ErrInvalidParam, "duplicate member %q", id)
		}
		seen[id] = true
	}
	if err := m.checkUserNow(user, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	for _, id := range ids {
		if !m.has(id) {
			return newErr(ErrNotFound, "member %q does not exist", id)
		}
	}
	for _, id := range ids {
		if m.elemGroup[id] != "" {
			return newErr(ErrInvalidTarget, "member %q already belongs to group %q", id, m.elemGroup[id])
		}
	}
	if err := m.checkMovingSetLocked(ids, user, now); err != nil {
		return err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
		m.elemGroup[id] = gid
	}
	m.groups[gid] = set
	m.rev++
	for _, id := range ids {
		m.elemRev[id] = m.rev
	}
	m.lastNow = now
	return nil
}

func (m *model) ungroup(user, gid string, now int64) error {
	if gid == "" {
		return newErr(ErrInvalidParam, "group id must be non-empty")
	}
	if err := m.checkUserNow(user, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if m.groups[gid] == nil {
		if m.has(gid) {
			return newErr(ErrInvalidTarget, "%q is an element, not a group", gid)
		}
		return newErr(ErrNotFound, "group %q does not exist", gid)
	}
	members := m.membersByRank(gid)
	if err := m.checkMovingSetLocked(members, user, now); err != nil {
		return err
	}
	delete(m.groups, gid)
	delete(m.locks, gid)
	m.rev++
	for _, id := range members {
		m.elemGroup[id] = ""
		m.elemRev[id] = m.rev
	}
	m.lastNow = now
	return nil
}

func (m *model) remove(user, target string, now int64) error {
	if target == "" {
		return newErr(ErrInvalidParam, "target must be non-empty")
	}
	if err := m.checkUserNow(user, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	isGroup := m.groups[target] != nil
	if !isGroup && !m.has(target) {
		return newErr(ErrNotFound, "target %q does not exist", target)
	}
	var involved []string
	if isGroup {
		involved = m.membersByRank(target)
	} else {
		involved = []string{target}
	}
	if err := m.checkMovingSetLocked(involved, user, now); err != nil {
		return err
	}
	m.rev++
	if isGroup {
		for _, id := range involved {
			m.elemRev[id] = m.rev
			m.removeFromOrder(id)
			delete(m.elemRev, id)
			delete(m.elemGroup, id)
			delete(m.locks, id)
		}
		delete(m.groups, target)
		delete(m.locks, target)
	} else {
		m.elemRev[target] = m.rev
		if g := m.elemGroup[target]; g != "" {
			delete(m.groups[g], target)
		}
		m.removeFromOrder(target)
		delete(m.elemRev, target)
		delete(m.elemGroup, target)
		delete(m.locks, target)
	}
	m.lastNow = now
	return nil
}

func (m *model) removeFromOrder(id string) {
	i := m.rankOf(id)
	m.order = append(m.order[:i], m.order[i+1:]...)
}

func (m *model) reorder(user, target, anchor string, side Side, baseRev uint64, now int64) error {
	if target == "" || anchor == "" {
		return newErr(ErrInvalidParam, "target and anchor must be non-empty")
	}
	if side != Below && side != Above {
		return newErr(ErrInvalidParam, "invalid side %d", side)
	}
	if err := m.checkUserNow(user, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	targetIsGroup := m.groups[target] != nil
	if !targetIsGroup && !m.has(target) {
		return newErr(ErrNotFound, "target %q does not exist", target)
	}
	if !m.has(anchor) {
		if m.groups[anchor] != nil {
			return newErr(ErrInvalidTarget, "anchor %q is a group, must be an element", anchor)
		}
		return newErr(ErrNotFound, "anchor %q does not exist", anchor)
	}
	var moving []string
	if targetIsGroup {
		moving = m.membersByRank(target)
	} else {
		if g := m.elemGroup[target]; g != "" {
			return newErr(ErrInvalidTarget, "element %q belongs to group %q, reorder the group instead", target, g)
		}
		moving = []string{target}
	}
	for _, id := range moving {
		if id == anchor {
			return newErr(ErrInvalidTarget, "anchor %q is inside the moving set", anchor)
		}
	}
	if err := m.checkMovingSetLocked(moving, user, now); err != nil {
		return err
	}
	for _, id := range moving {
		if m.elemRev[id] > baseRev {
			return newErr(ErrConflict, "element %q affected by rev %d > baseRev %d", id, m.elemRev[id], baseRev)
		}
	}
	// 段移动：保持相对次序抽出，插入 anchor 上/下侧。
	inSet := make(map[string]bool, len(moving))
	for _, id := range moving {
		inSet[id] = true
	}
	var rest, seg []string
	for _, id := range m.order {
		if inSet[id] {
			seg = append(seg, id)
		} else {
			rest = append(rest, id)
		}
	}
	pos := -1
	for i, id := range rest {
		if id == anchor {
			pos = i
		}
	}
	if side == Above {
		pos++
	}
	next := make([]string, 0, len(m.order))
	next = append(next, rest[:pos]...)
	next = append(next, seg...)
	next = append(next, rest[pos:]...)
	m.order = next
	m.rev++
	for _, id := range moving {
		m.elemRev[id] = m.rev
	}
	m.lastNow = now
	return nil
}

func (m *model) lock(user, id string, ttl int64, now int64) error {
	if id == "" {
		return newErr(ErrInvalidParam, "id must be non-empty")
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return newErr(ErrInvalidParam, "ttl %d out of range [%d,%d]", ttl, MinTTL, MaxTTL)
	}
	if err := m.checkUserNow(user, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	isGroup := m.groups[id] != nil
	if !isGroup && !m.has(id) {
		return newErr(ErrNotFound, "lock target %q does not exist", id)
	}
	related := []string{id}
	if isGroup {
		members := make([]string, 0, len(m.groups[id]))
		for x := range m.groups[id] {
			members = append(members, x)
		}
		sort.Strings(members)
		related = append(related, members...)
	} else if g := m.elemGroup[id]; g != "" {
		related = append(related, g)
	}
	for _, rid := range related {
		if l, ok := m.activeLock(rid, now); ok && l.owner != user {
			return lockedErr(l.owner, l.expireAt-now, "cannot lock %q: %q is locked by another user", id, rid)
		}
	}
	m.locks[id] = mLock{owner: user, expireAt: now + ttl}
	m.lastNow = now
	return nil
}

func (m *model) unlock(user, id string, now int64) error {
	if id == "" {
		return newErr(ErrInvalidParam, "id must be non-empty")
	}
	if err := m.checkUserNow(user, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if !m.has(id) && m.groups[id] == nil {
		return newErr(ErrNotFound, "unlock target %q does not exist", id)
	}
	l, ok := m.activeLock(id, now)
	if !ok {
		return newErr(ErrInvalidTarget, "no active lock on %q", id)
	}
	if l.owner != user {
		return lockedErr(l.owner, l.expireAt-now, "cannot unlock %q held by another user", id)
	}
	delete(m.locks, id)
	m.lastNow = now
	return nil
}
