package ontology

import "fmt"

// DerivedState 是依赖某条链接存在性的派生状态。
// 链接进入待处理时它被标记为暂时不可信（Stale），但不被清空或删除；
// 链接最终删除时它被清理；链接恢复有效或转为保留时它恢复可信。
type DerivedState struct {
	ID          string
	LinkID      string
	Payload     any
	Stale       bool
	StaleReason string
}

// DerivedView 是派生状态的查询结果，显式携带不可信标注。
type DerivedView struct {
	DerivedState
}

// RegisterDerived 为一条链接登记派生状态，返回派生状态 ID。
func (m *Manager) RegisterDerived(linkID string, payload any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[linkID]; !ok {
		return "", ErrLinkNotFound
	}
	m.seq++
	id := fmt.Sprintf("derived-%d", m.seq)
	d := &DerivedState{ID: id, LinkID: linkID, Payload: payload}
	m.derived[id] = d
	m.linkDeriv[linkID] = append(m.linkDeriv[linkID], id)
	return id, nil
}

// QueryDerived 查询派生状态。
// 处于暂时不可信期间的派生状态仍然返回其内容，但 Stale 为 true 且
// 附带 StaleReason，调用方不会被返回看似正常但依据已过期的结果。
// 已被清理（随链接最终删除）的派生状态返回 ErrDerivedNotFound。
func (m *Manager) QueryDerived(id string) (DerivedView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.derived[id]
	if !ok {
		return DerivedView{}, ErrDerivedNotFound
	}
	return DerivedView{DerivedState: *d}, nil
}

// markDerivedStaleLocked 将链接的全部派生状态标记为暂时不可信。
func (m *Manager) markDerivedStaleLocked(linkID, reason string) {
	for _, id := range m.linkDeriv[linkID] {
		if d, ok := m.derived[id]; ok {
			d.Stale = true
			d.StaleReason = reason
		}
	}
}

// restoreDerivedLocked 将链接的全部派生状态恢复为可信。
func (m *Manager) restoreDerivedLocked(linkID string) {
	for _, id := range m.linkDeriv[linkID] {
		if d, ok := m.derived[id]; ok {
			d.Stale = false
			d.StaleReason = ""
		}
	}
}

// cleanDerivedLocked 清理链接的全部派生状态（链接最终删除时调用）。
func (m *Manager) cleanDerivedLocked(linkID string) {
	for _, id := range m.linkDeriv[linkID] {
		delete(m.derived, id)
	}
	delete(m.linkDeriv, linkID)
}
