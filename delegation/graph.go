package delegation

import "sort"

// outEdges 返回某主体就某权限向外发出的委托（按 ID 排序保证确定性）。
func (m *Manager) outEdges(from Subject, p Permission) []*Edge {
	var result []*Edge
	for _, e := range m.edges {
		if e.Delegator == from && e.Permission == p {
			result = append(result, e)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// findParent 为委托 e 解析授权来源边：委托者持有的、当前仍有效的同类委托。
func (m *Manager) findParent(e *Edge) *Edge {
	for _, candidate := range m.edges {
		if candidate.Delegatee != e.Delegator || candidate.Permission != e.Permission {
			continue
		}
		if m.chainActive(candidate) {
			return candidate
		}
	}
	return nil
}

// chainActive 判断从该边向上回溯到根的整条链是否有效：
// 未撤销、未过期、且每一级都允许再委托。
func (m *Manager) chainActive(e *Edge) bool {
	now := m.now()
	visited := map[string]bool{}
	cur := e
	for cur != nil {
		if visited[cur.ID] {
			return false
		}
		visited[cur.ID] = true
		if cur.status != statusActive || !cur.ExpiresAt.After(now) {
			return false
		}
		if cur.ParentID == "" {
			return m.roots[cur.Delegator][cur.Permission]
		}
		parent := m.edges[cur.ParentID]
		if parent == nil {
			return false
		}
		if !parent.CanRedelegate {
			return false
		}
		if parent.status != statusActive || !parent.ExpiresAt.After(now) {
			return false
		}
		cur = parent
	}
	return false
}

// createsCycle 判断把 e（尚未入图）加入后，是否会在 e.Delegatee 沿同类委托边
// 能重新到达 e.Delegator，从而形成环（含委托者与受托者相同的自环）。
func (m *Manager) createsCycle(e *Edge) bool {
	if e.Delegator == e.Delegatee {
		return true
	}
	target := e.Delegator
	seen := map[Subject]bool{e.Delegatee: true}
	stack := []Subject{e.Delegatee}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range m.outEdges(cur, e.Permission) {
			if next.Delegatee == target {
				return true
			}
			if !seen[next.Delegatee] {
				seen[next.Delegatee] = true
				stack = append(stack, next.Delegatee)
			}
		}
	}
	return false
}

// recompute 以当前图重算全部非撤销边的状态，保证最终判定与注册顺序无关。
func (m *Manager) recompute() {
	now := m.now()
	ids := make([]string, 0, len(m.edges))
	for id := range m.edges {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	changed := true
	for changed {
		changed = false
		for _, id := range ids {
			e := m.edges[id]
			if e.status == statusRevoked {
				continue
			}
			newStatus := statusActive
			var reason RejectReason

			if !e.ExpiresAt.After(now) {
				newStatus, reason = statusMissingAuthority, ReasonExpired
			} else if e.ParentID == "" {
				if !m.roots[e.Delegator][e.Permission] {
					newStatus, reason = statusMissingAuthority, ReasonNoAuthority
				}
			} else {
				parent := m.edges[e.ParentID]
				if parent == nil {
					newStatus, reason = statusMissingAuthority, ReasonNoAuthority
				} else if !parent.CanRedelegate {
					newStatus, reason = statusInvalidRedelegate, ReasonRedelegateForbidden
				} else if parent.status == statusRevoked ||
					parent.status == statusMissingAuthority ||
					parent.status == statusInvalidRedelegate {
					newStatus, reason = statusMissingAuthority, ReasonNoAuthority
				}
			}

			if e.status != newStatus || e.invalidity != reason {
				e.status = newStatus
				e.invalidity = reason
				changed = true
			}
		}
	}
}
