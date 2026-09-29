package delegation

// HasPermission 判定主体当前是否有效持有指定权限。
// 返回判定依据字符串：根授权、有效委托链路径、或失效原因。
func (m *Manager) HasPermission(s Subject, p Permission) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.roots[s][p] {
		basis := "ROOT_GRANT:" + string(s) + " holds " + string(p) + " directly"
		m.log("evaluate subject=%s permission=%s decision=ALLOW basis=%q", s, p, basis)
		return true, basis
	}

	best := ""
	for _, e := range m.outEdgesLocked(s, p) {
		ok, basis := m.evalEdge(e)
		if ok {
			m.log("evaluate subject=%s permission=%s decision=ALLOW basis=%q via=%s", s, p, basis, e.ID)
			return true, basis
		}
		if best == "" {
			best = basis
		}
	}

	if best == "" {
		best = "NO_GRANT:" + string(s) + " never received " + string(p)
	}
	m.log("evaluate subject=%s permission=%s decision=DENY basis=%q", s, p, best)
	return false, best
}

// EffectivePermissions 返回主体当前有效持有的全部权限及各自判定依据。
func (m *Manager) EffectivePermissions(s Subject) map[Permission]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := map[Permission]string{}
	for p := range m.roots[s] {
		result[p] = "ROOT_GRANT:" + string(s) + " holds " + string(p) + " directly"
	}
	incoming := map[Permission][]*Edge{}
	for _, e := range m.edges {
		if e.Delegatee == s {
			incoming[e.Permission] = append(incoming[e.Permission], e)
		}
	}
	for p, edges := range incoming {
		for _, e := range edges {
			if ok, basis := m.evalEdge(e); ok {
				result[p] = basis
				break
			}
		}
	}
	m.log("evaluate subject=%s decision=SUMMARY effective_permissions=%d", s, len(result))
	return result
}

func (m *Manager) outEdgesLocked(to Subject, p Permission) []*Edge {
	var result []*Edge
	for _, e := range m.edges {
		if e.Delegatee == to && e.Permission == p {
			result = append(result, e)
		}
	}
	return result
}

// evalEdge 沿 ParentID 回溯，给出该委托边是否有效以及可追溯到根的依据。
func (m *Manager) evalEdge(e *Edge) (bool, string) {
	now := m.now()
	chain := []*Edge{e}
	visited := map[string]bool{e.ID: true}
	cur := e

	for cur.ParentID != "" {
		parent := m.edges[cur.ParentID]
		if parent == nil {
			return false, "BROKEN_CHAIN: parent " + cur.ParentID + " missing for " + cur.ID
		}
		if visited[parent.ID] {
			return false, "CYCLE: chain through " + parent.ID + " is cyclic"
		}
		visited[parent.ID] = true
		chain = append(chain, parent)
		cur = parent
	}

	if !m.roots[cur.Delegator][cur.Permission] {
		return false, "NO_ROOT: chain for " + e.ID + " does not reach a root grant"
	}

	for i := len(chain) - 1; i >= 0; i-- {
		link := chain[i]
		if link.status == statusRevoked {
			return false, "REVOKED: delegation " + link.ID + " was revoked"
		}
		if !link.ExpiresAt.After(now) {
			return false, "EXPIRED: delegation " + link.ID + " expired at " + link.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
		}
		if i < len(chain)-1 && !chain[i+1].CanRedelegate {
			return false, "REDELEGATE_FORBIDDEN: " + chain[i+1].ID + " forbids redelegation"
		}
	}

	path := chainPath(chain)
	return true, "DELEGATION_CHAIN:" + path
}

func chainPath(chain []*Edge) string {
	s := ""
	for i := len(chain) - 1; i >= 0; i-- {
		link := chain[i]
		if s == "" {
			s = string(link.Delegator)
		}
		s += " --" + link.ID + "(" + string(link.Permission) + ")--> " + string(link.Delegatee)
	}
	return s
}
