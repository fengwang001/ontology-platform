package ontology

import (
	"fmt"
	"sort"
)

// CreateLink 创建一条链接。若任一端对象在对应方向的当前有效上限已满，
// 则拒绝并记录 EventCreateRejected；待处理链接不参与该判断。
func (m *Manager) CreateLink(typeID, linkID, source, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLinkLocked(typeID, linkID, source, target)
}

func (m *Manager) createLinkLocked(typeID, linkID, source, target string) error {
	lt, ok := m.types[typeID]
	if !ok {
		return ErrLinkTypeNotFound
	}
	if _, ok := m.links[linkID]; ok {
		return ErrLinkExists
	}
	if _, ok := m.deleted[linkID]; ok {
		return ErrLinkExists
	}
	if _, ok := m.objects[source]; !ok {
		return ErrObjectNotFound
	}
	if _, ok := m.objects[target]; !ok {
		return ErrObjectNotFound
	}
	// 基数判断只统计有效链接；待处理链接不参与。
	for _, dir := range []Direction{DirectionOut, DirectionIn} {
		objID := source
		if dir == DirectionIn {
			objID = target
		}
		gk := groupKey{typeID, dir, objID}
		g := m.groups[gk]
		if g == nil {
			continue
		}
		eff := m.effectiveLimit(lt, g, dir)
		if eff != Unlimited {
			active := len(g.links) - len(g.pendingList)
			if active >= eff {
				m.emit(AuditEvent{
					Kind:        EventCreateRejected,
					LinkID:      linkID,
					TypeID:      typeID,
					Direction:   dir,
					ObjectID:    objID,
					Trigger:     "create_link",
					Basis:       fmt.Sprintf("active=%d effectiveLimit=%d", active, eff),
					Disposition: "rejected_limit_full",
					Limit:       eff,
					Registered:  len(g.links),
				})
				m.recordOp(appliedOp{op: "create", typeID: typeID, linkID: linkID, source: source, target: target, accepted: false})
				return ErrLimitFull
			}
		}
	}
	m.seq++
	l := &Link{ID: linkID, TypeID: typeID, Source: source, Target: target, Seq: m.seq}
	m.links[linkID] = l
	for _, dir := range []Direction{DirectionOut, DirectionIn} {
		objID := source
		if dir == DirectionIn {
			objID = target
		}
		gk := groupKey{typeID, dir, objID}
		g := m.groups[gk]
		if g == nil {
			g = newGroup()
			m.groups[gk] = g
		}
		g.links = append(g.links, linkID)
		g.totalEver++
	}
	m.emit(AuditEvent{
		Kind:        EventLinkCreated,
		LinkID:      linkID,
		TypeID:      typeID,
		Trigger:     "create_link",
		Disposition: "accepted_active",
	})
	m.recordOp(appliedOp{op: "create", typeID: typeID, linkID: linkID, source: source, target: target, accepted: true})
	return nil
}

// SetLimit 在运行期调整某方向的基础上限。
// 下调时按确定性规则标记超额链接为待处理；
// 上调时按标记顺序的逆序恢复不再超额的待处理链接。
func (m *Manager) SetLimit(typeID string, dir Direction, newLimit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setLimitLocked(typeID, dir, newLimit)
}

func (m *Manager) setLimitLocked(typeID string, dir Direction, newLimit int) error {
	lt, ok := m.types[typeID]
	if !ok {
		return ErrLinkTypeNotFound
	}
	if newLimit < Unlimited || newLimit == 0 {
		return ErrInvalidLimit
	}
	old := lt.limits[dir]
	lt.limits[dir] = newLimit
	trigger := fmt.Sprintf("limit_changed %d->%d", old, newLimit)
	m.emit(AuditEvent{
		Kind:        EventLimitChanged,
		TypeID:      typeID,
		Direction:   dir,
		Trigger:     trigger,
		Disposition: "limit_updated",
		Limit:       newLimit,
	})
	// 按对象 ID 排序遍历登记组，保证审计事件顺序确定。
	var keys []groupKey
	for gk := range m.groups {
		if gk.typeID == typeID && gk.dir == dir {
			keys = append(keys, gk)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].objID < keys[j].objID })
	for _, gk := range keys {
		g := m.groups[gk]
		g.adjustments++
		m.recomputeLocked(lt, gk, g, trigger)
	}
	m.recordOp(appliedOp{op: "setlimit", typeID: typeID, dir: dir, limit: newLimit, accepted: true})
	return nil
}

// recomputeLocked 依据当前有效上限重新计算登记组的待处理集合：
// 目标待处理集合 = 已登记且未被保留固定的链接中按 (Seq, ID) 最新的 excess 条，
// 其中 excess = max(0, registered - effectiveLimit)。
// 该函数是轨迹无关性的核心：待处理集合只取决于当前有效上限与登记集合，
// 与历史上限调整轨迹无关。
func (m *Manager) recomputeLocked(lt *linkType, gk groupKey, g *group, trigger string) {
	eff := m.effectiveLimit(lt, g, gk.dir)
	excess := 0
	if eff != Unlimited && len(g.links) > eff {
		excess = len(g.links) - eff
	}
	// 确定性选择规则：按 (Seq, ID) 升序（g.links 即按此序维护），
	// 跳过保留固定的链接，取最新的 excess 条作为目标待处理集合。
	desired := make(map[string]struct{}, excess)
	for i := len(g.links) - 1; i >= 0 && len(desired) < excess; i-- {
		id := g.links[i]
		if _, ok := g.pinned[id]; ok {
			continue
		}
		desired[id] = struct{}{}
	}
	// 恢复：按标记顺序的逆序（pendingList 从后往前）恢复不再超额的链接。
	if len(g.pendingList) > 0 {
		kept := g.pendingList[:0]
		var restored []string
		for _, id := range g.pendingList {
			if _, ok := desired[id]; ok {
				kept = append(kept, id)
			} else {
				delete(g.pendingSet, id)
				restored = append(restored, id)
			}
		}
		g.pendingList = kept
		for i := len(restored) - 1; i >= 0; i-- {
			id := restored[i]
			l := m.links[id]
			basis := l.MarkBasis
			// 仅当链接在任一方向都不再待处理时，才清除标记依据并恢复派生状态。
			if !m.isPendingLocked(l) {
				l.MarkBasis = ""
				m.restoreDerivedLocked(id)
			}
			m.emit(AuditEvent{
				Kind:        EventPendingRestored,
				LinkID:      id,
				TypeID:      gk.typeID,
				Direction:   gk.dir,
				ObjectID:    gk.objID,
				Trigger:     trigger,
				Basis:       basis,
				Disposition: "restored_active",
				Limit:       eff,
				Registered:  len(g.links),
			})
		}
	}
	// 标记：按 (Seq, ID) 逆序（最新优先）标记新进入超额的链接。
	for i := len(g.links) - 1; i >= 0; i-- {
		id := g.links[i]
		if _, ok := desired[id]; !ok {
			continue
		}
		if _, ok := g.pendingSet[id]; ok {
			continue
		}
		g.pendingSet[id] = struct{}{}
		g.pendingList = append(g.pendingList, id)
		l := m.links[id]
		l.MarkBasis = fmt.Sprintf(
			"deterministic:newest-first key=(seq:%d,id:%s) registered=%d effectiveLimit=%d",
			l.Seq, l.ID, len(g.links), eff)
		m.markDerivedStaleLocked(id, "link pending: excess over cardinality limit")
		m.emit(AuditEvent{
			Kind:        EventExcessMarked,
			LinkID:      id,
			TypeID:      gk.typeID,
			Direction:   gk.dir,
			ObjectID:    gk.objID,
			Trigger:     trigger,
			Basis:       l.MarkBasis,
			Disposition: "marked_pending",
			Limit:       eff,
			Registered:  len(g.links),
		})
	}
}
