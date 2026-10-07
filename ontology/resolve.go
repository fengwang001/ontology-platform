package ontology

import "fmt"

// groupMembership 是链接所属登记组的一次引用。
type groupMembership struct {
	key   groupKey
	group *group
}

// groupsOfLocked 返回链接在两个方向上所属的登记组。
func (m *Manager) groupsOfLocked(l *Link) []groupMembership {
	var out []groupMembership
	for _, dir := range []Direction{DirectionOut, DirectionIn} {
		objID := l.Source
		if dir == DirectionIn {
			objID = l.Target
		}
		gk := groupKey{l.TypeID, dir, objID}
		if g, ok := m.groups[gk]; ok {
			out = append(out, groupMembership{gk, g})
		}
	}
	return out
}

// isPendingLocked 判断链接是否在任一方向登记组中处于待处理。
func (m *Manager) isPendingLocked(l *Link) bool {
	for _, gm := range m.groupsOfLocked(l) {
		if _, ok := gm.group.pendingSet[l.ID]; ok {
			return true
		}
	}
	return false
}

// endpointRevokedLocked 判断链接任一端对象是否已被撤销或不存在。
func (m *Manager) endpointRevokedLocked(l *Link) bool {
	for _, id := range [2]string{l.Source, l.Target} {
		obj, ok := m.objects[id]
		if !ok || obj.revoked {
			return true
		}
	}
	return false
}

// ResolvePending 对待处理链接执行显式后续处理动作。
//
// ResolveKeep：校验链接两端对象仍然存在且未被撤销，然后将链接恢复为
// 有效，并相应提升该方向该对象登记组的有效上限（keepOverride）。
// ResolveDelete：将链接最终删除，并清理其关联的派生状态。
// 若任一端对象已被撤销，保留动作被拒绝（ErrObjectRevoked）。
func (m *Manager) ResolvePending(linkID string, res Resolution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[linkID]
	if !ok {
		return ErrLinkNotFound
	}
	if !m.isPendingLocked(l) {
		return ErrNotPending
	}
	lt := m.types[l.TypeID]
	switch res {
	case ResolveKeep:
		// 保留条件校验：两端对象必须仍然存在且未被撤销。
		if m.endpointRevokedLocked(l) {
			return ErrObjectRevoked
		}
		for _, gm := range m.groupsOfLocked(l) {
			g := gm.group
			if _, ok := g.pendingSet[linkID]; !ok {
				continue
			}
			delete(g.pendingSet, linkID)
			g.pinned[linkID] = struct{}{}
			g.keepOverride++
			removeID(&g.pendingList, linkID)
			eff := m.effectiveLimit(lt, g, gm.key.dir)
			m.emit(AuditEvent{
				Kind:        EventPendingKept,
				LinkID:      linkID,
				TypeID:      l.TypeID,
				Direction:   gm.key.dir,
				ObjectID:    gm.key.objID,
				Trigger:     "resolve_keep",
				Basis:       l.MarkBasis,
				Disposition: fmt.Sprintf("kept_active; effective_limit_raised_to=%d", eff),
				Limit:       eff,
				Registered:  len(g.links),
			})
			m.recomputeLocked(lt, gm.key, g, "resolve_keep")
		}
		l.MarkBasis = ""
		m.restoreDerivedLocked(linkID)
		return nil
	case ResolveDelete:
		m.deleteLinkLocked(l, lt, EventPendingDeleted, "resolve_delete", "deleted")
		return nil
	}
	return nil
}

// FinalizePending 是待处理链接的默认处理路径（孤儿清理）：
//  1. 若任一端对象已被撤销 —— 强制转为删除（EventPendingForceDeleted），
//     该判定优先于一般规则；
//  2. 否则按一般规则：在当前有效上限下已不超额的恢复有效，
//     仍然超额的转为删除（EventPendingDeleted）。
func (m *Manager) FinalizePending(linkID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[linkID]
	if !ok {
		return ErrLinkNotFound
	}
	if !m.isPendingLocked(l) {
		return ErrNotPending
	}
	lt := m.types[l.TypeID]
	// 优先判定：所依赖对象已被撤销 -> 只能转为删除。
	if m.endpointRevokedLocked(l) {
		m.deleteLinkLocked(l, lt, EventPendingForceDeleted,
			"finalize:endpoint_object_revoked", "force_deleted_object_revoked")
		return nil
	}
	// 一般规则：任一方向上仍然超额 -> 转为删除。
	for _, gm := range m.groupsOfLocked(l) {
		if _, ok := gm.group.pendingSet[linkID]; !ok {
			continue
		}
		if m.inExcessLocked(lt, gm.key, gm.group, linkID) {
			m.deleteLinkLocked(l, lt, EventPendingDeleted,
				"finalize:still_excess", "deleted_still_excess")
			return nil
		}
	}
	// 已不超额：从所有登记组的待处理集合中移除并恢复为有效。
	for _, gm := range m.groupsOfLocked(l) {
		g := gm.group
		if _, ok := g.pendingSet[linkID]; !ok {
			continue
		}
		delete(g.pendingSet, linkID)
		removeID(&g.pendingList, linkID)
		m.emit(AuditEvent{
			Kind:        EventPendingRestored,
			LinkID:      linkID,
			TypeID:      l.TypeID,
			Direction:   gm.key.dir,
			ObjectID:    gm.key.objID,
			Trigger:     "finalize:no_longer_excess",
			Basis:       l.MarkBasis,
			Disposition: "restored_active",
			Limit:       m.effectiveLimit(lt, g, gm.key.dir),
			Registered:  len(g.links),
		})
	}
	l.MarkBasis = ""
	m.restoreDerivedLocked(linkID)
	return nil
}

// FinalizeAllPending 对某链接类型全部待处理链接执行默认处理路径，
// 返回成功处理的链接数。处理顺序按 (Seq, ID) 确定。
func (m *Manager) FinalizeAllPending(typeID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.types[typeID]; !ok {
		return 0, ErrLinkTypeNotFound
	}
	seen := make(map[string]struct{})
	var ids []string
	for gk, g := range m.groups {
		if gk.typeID != typeID {
			continue
		}
		for _, id := range g.pendingList {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	m.sortLinkIDs(ids)
	n := 0
	for _, id := range ids {
		l, ok := m.links[id]
		if !ok || !m.isPendingLocked(l) {
			continue
		}
		lt := m.types[l.TypeID]
		if m.endpointRevokedLocked(l) {
			m.deleteLinkLocked(l, lt, EventPendingForceDeleted,
				"finalize:endpoint_object_revoked", "force_deleted_object_revoked")
		} else {
			m.deleteLinkLocked(l, lt, EventPendingDeleted,
				"finalize:still_excess", "deleted_still_excess")
		}
		n++
	}
	return n, nil
}

// inExcessLocked 判断链接在当前有效上限下是否属于目标超额集合。
func (m *Manager) inExcessLocked(lt *linkType, gk groupKey, g *group, linkID string) bool {
	eff := m.effectiveLimit(lt, g, gk.dir)
	if eff == Unlimited || len(g.links) <= eff {
		return false
	}
	excess := len(g.links) - eff
	count := 0
	for i := len(g.links) - 1; i >= 0 && count < excess; i-- {
		id := g.links[i]
		if _, ok := g.pinned[id]; ok {
			continue
		}
		if id == linkID {
			return true
		}
		count++
	}
	return false
}

// deleteLinkLocked 将链接最终删除：移出全部登记组、清理派生状态、
// 留存审计信息，并对受影响的登记组重算待处理集合。
func (m *Manager) deleteLinkLocked(l *Link, lt *linkType, kind EventKind, trigger, disposition string) {
	groups := m.groupsOfLocked(l)
	for _, gm := range groups {
		g := gm.group
		delete(g.pendingSet, l.ID)
		delete(g.pinned, l.ID)
		removeID(&g.links, l.ID)
		removeID(&g.pendingList, l.ID)
		m.emit(AuditEvent{
			Kind:        kind,
			LinkID:      l.ID,
			TypeID:      l.TypeID,
			Direction:   gm.key.dir,
			ObjectID:    gm.key.objID,
			Trigger:     trigger,
			Basis:       l.MarkBasis,
			Disposition: disposition,
			Limit:       m.effectiveLimit(lt, g, gm.key.dir),
			Registered:  len(g.links),
		})
	}
	delete(m.links, l.ID)
	m.deleted[l.ID] = l
	m.cleanDerivedLocked(l.ID)
	for _, gm := range groups {
		m.recomputeLocked(lt, gm.key, gm.group, trigger)
	}
}

// removeID 从切片中删除首个匹配元素。
func removeID(ids *[]string, id string) {
	s := *ids
	for i, v := range s {
		if v == id {
			*ids = append(s[:i], s[i+1:]...)
			return
		}
	}
}
