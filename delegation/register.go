package delegation

import (
	"sort"
	"time"
)

func validateEdge(e Edge) *RejectError {
	if e.ID == "" {
		return newReject("register", ReasonUnknown, "empty delegation id")
	}
	if e.Delegator == "" || e.Delegatee == "" || e.Permission == "" {
		return newReject("register", ReasonUnknown, "delegator, delegatee and permission are required")
	}
	if e.Delegator == e.Delegatee {
		return newReject("register", ReasonSelfDelegation,
			"%s cannot delegate %s to itself", e.Delegator, e.Permission)
	}
	return nil
}

// Register 注册一条委托。
//
// 拒绝原因（错误可用 AsReject 区分）：
//   - ReasonDuplicate：委托 ID 已存在
//   - ReasonExpired：有效期已过
//   - ReasonNoAuthority：委托者当前不持有该权限（无可溯及根的有效来源）
//   - ReasonRedelegateForbidden：来源委托标记为不可再委托
//   - ReasonCycle：该委托会使委托链成环
//
// 任何拒绝都不会改变已有委托。
func (m *Manager) Register(e Edge) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rej := validateEdge(e); rej != nil {
		m.log("register REJECTED id=%s delegator=%s delegatee=%s permission=%s reason=%s detail=%q",
			e.ID, e.Delegator, e.Delegatee, e.Permission, reasonName(rej.Reason), rej.Detail)
		return rej
	}
	if _, exists := m.edges[e.ID]; exists {
		rej := newReject("register", ReasonDuplicate, "delegation id %q already exists", e.ID)
		m.log("register REJECTED id=%s delegator=%s delegatee=%s permission=%s reason=%s",
			e.ID, e.Delegator, e.Delegatee, e.Permission, reasonName(rej.Reason))
		return rej
	}
	if !e.ExpiresAt.After(m.now()) {
		rej := newReject("register", ReasonExpired,
			"expires_at %s is not in the future", e.ExpiresAt.Format(time.RFC3339))
		m.log("register REJECTED id=%s delegator=%s delegatee=%s permission=%s reason=%s",
			e.ID, e.Delegator, e.Delegatee, e.Permission, reasonName(rej.Reason))
		return rej
	}

	candidate := e
	candidate.status = statusActive
	stored := &candidate
	m.edges[e.ID] = stored

	if m.createsCycle(stored) {
		delete(m.edges, e.ID)
		rej := newReject("register", ReasonCycle,
			"delegation %s -> %s on %s would close a cycle", e.Delegator, e.Delegatee, e.Permission)
		m.log("register REJECTED id=%s delegator=%s delegatee=%s permission=%s reason=%s",
			e.ID, e.Delegator, e.Delegatee, e.Permission, reasonName(rej.Reason))
		return rej
	}

	if m.roots[e.Delegator][e.Permission] {
		stored.ParentID = ""
	} else {
		parent := m.findParent(stored)
		if parent == nil {
			delete(m.edges, e.ID)
			rej := newReject("register", ReasonNoAuthority,
				"delegator %s holds no active grant for %s", e.Delegator, e.Permission)
			m.log("register REJECTED id=%s delegator=%s delegatee=%s permission=%s reason=%s",
				e.ID, e.Delegator, e.Delegatee, e.Permission, reasonName(rej.Reason))
			return rej
		}

		if !parent.CanRedelegate {
			delete(m.edges, e.ID)
			rej := newReject("register", ReasonRedelegateForbidden,
				"source delegation %s for %s forbids redelegation", parent.ID, e.Permission)
			m.log("register REJECTED id=%s delegator=%s delegatee=%s permission=%s reason=%s",
				e.ID, e.Delegator, e.Delegatee, e.Permission, reasonName(rej.Reason))
			return rej
		}

		stored.ParentID = parent.ID
	}
	m.recompute()

	if stored.status != statusActive {
		reason := stored.invalidity
		delete(m.edges, e.ID)
		m.recompute()
		rej := newReject("register", reason,
			"delegator %s cannot delegate %s", e.Delegator, e.Permission)
		m.log("register REJECTED id=%s delegator=%s delegatee=%s permission=%s reason=%s",
			e.ID, e.Delegator, e.Delegatee, e.Permission, reasonName(reason))
		return rej
	}

	m.log("register ACCEPTED id=%s delegator=%s delegatee=%s permission=%s expires_at=%s can_redelegate=%t via=%s",
		stored.ID, stored.Delegator, stored.Delegatee, stored.Permission,
		stored.ExpiresAt.Format(time.RFC3339), stored.CanRedelegate, stored.ParentID)
	return nil
}

// RegisterSet 一次性注册一组委托。组内允许以任意顺序给出：方法会先做环检测，
// 再按授权依赖排序解析，因此同一组委托无论输入顺序如何，
// 最终接受/拒绝结果与生效权限完全一致。
func (m *Manager) RegisterSet(edges []Edge) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rej := m.probeSet(edges); rej != nil {
		m.log("register-set REJECTED reason=%s detail=%q", reasonName(rej.Reason), rej.Detail)
		return rej
	}

	m.log("register-set ACCEPTED count=%d", len(edges))
	return nil
}

// probeSet 在不修改 Manager 状态的前提下，模拟整组委托加入后的图，
// 返回第一个可确定的拒绝原因；为 nil 表示整组有效并已落库。
func (m *Manager) probeSet(edges []Edge) *RejectError {
	working := make(map[string]*Edge, len(m.edges)+len(edges))
	for id, e := range m.edges {
		copy := *e
		working[id] = &copy
	}

	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		if rej := validateEdge(e); rej != nil {
			return rej
		}
		if _, exists := m.edges[e.ID]; exists {
			return newReject("register-set", ReasonDuplicate, "delegation id %q already exists", e.ID)
		}
		if _, dup := working[e.ID]; dup {
			return newReject("register-set", ReasonDuplicate, "delegation id %q duplicated in batch", e.ID)
		}
		if !e.ExpiresAt.After(m.now()) {
			return newReject("register-set", ReasonExpired,
				"delegation %q expires_at %s is not in the future", e.ID, e.ExpiresAt.Format(time.RFC3339))
		}
		copy := e
		copy.status = statusActive
		working[e.ID] = &copy
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)

	if rej := m.resolveParents(working, ids); rej != nil {
		return rej
	}

	// 父边解析后检测环。
	revive := m.edges
	m.edges = working
	for _, id := range ids {
		e := working[id]
		// 临时摘除后检测：createsCycle 基于其余边。
		delete(working, id)
		if m.createsCycle(e) {
			m.edges = revive
			return newReject("register-set", ReasonCycle,
				"delegation %s -> %s on %s would close a cycle", e.Delegator, e.Delegatee, e.Permission)
		}
		working[id] = e
	}
	m.edges = revive

	// 提交：按确定顺序重放单条注册的图计算。
	for _, id := range ids {
		e := working[id]
		stored := *e
		m.edges[id] = &stored
	}
	m.recompute()
	for _, id := range ids {
		if m.edges[id].status != statusActive {
			reason := m.edges[id].invalidity
			m.rollback(ids)
			return newReject("register-set", reason, "delegation %q invalid in resolved graph", id)
		}
	}
	return nil
}

// resolveParents 反复为待解析边寻找授权来源（根权限或已解析的上游委托），
// 使输入顺序不影响解析结果。
func (m *Manager) resolveParents(working map[string]*Edge, pending []string) *RejectError {
	pendingSet := make(map[string]bool, len(pending))
	for _, id := range pending {
		pendingSet[id] = true
	}

	for len(pendingSet) > 0 {
		progress := false
		ids := make([]string, 0, len(pendingSet))
		for id := range pendingSet {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		for _, id := range ids {
			e := working[id]
			if m.roots[e.Delegator][e.Permission] {
				e.ParentID = ""
				delete(pendingSet, id)
				progress = true
				continue
			}
			var candidates []*Edge
			for _, candidate := range working {
				if candidate.ID == id ||
					candidate.Delegatee != e.Delegator ||
					candidate.Permission != e.Permission ||
					candidate.status == statusRevoked {
					continue
				}
				if pendingSet[candidate.ID] {
					continue
				}
				candidates = append(candidates, candidate)
			}
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
			for _, candidate := range candidates {
				if candidate.status == statusActive && candidate.CanRedelegate {
					e.ParentID = candidate.ID
					delete(pendingSet, id)
					progress = true
					break
				}
			}
		}

		if !progress {
			for _, id := range ids {
				e := working[id]
				// 区分：存在不可再委托来源 vs. 完全无来源。
				forbidden := false
				for _, candidate := range working {
					if candidate.Delegatee == e.Delegator &&
						candidate.Permission == e.Permission &&
						candidate.status == statusActive &&
						!candidate.CanRedelegate {
						forbidden = true
					}
				}
				if forbidden {
					return newReject("register-set", ReasonRedelegateForbidden,
						"delegator %s received %s without redelegation right", e.Delegator, e.Permission)
				}
				return newReject("register-set", ReasonNoAuthority,
					"delegator %s holds no active grant for %s", e.Delegator, e.Permission)
			}
		}
	}
	return nil
}

func (m *Manager) rollback(newIDs []string) {
	for _, id := range newIDs {
		delete(m.edges, id)
	}
	m.recompute()
}

// Revoke 撤销指定委托；该边及其全部下游边永久失效。
func (m *Manager) Revoke(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.edges[id]
	if !ok {
		m.log("revoke REJECTED id=%s reason=NOT_FOUND", id)
		return newReject("revoke", ReasonUnknown, "delegation %q not found", id)
	}

	e.status = statusRevoked
	e.invalidity = ReasonUnknown
	queue := []string{id}
	downstream := map[string]bool{id: true}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		for _, other := range m.edges {
			if other.ParentID == parentID && !downstream[other.ID] {
				other.status = statusRevoked
				other.invalidity = ReasonNoAuthority
				downstream[other.ID] = true
				queue = append(queue, other.ID)
			}
		}
	}

	m.log("revoke ACCEPTED id=%s delegator=%s delegatee=%s permission=%s cascade=%d",
		e.ID, e.Delegator, e.Delegatee, e.Permission, len(downstream)-1)
	return nil
}
