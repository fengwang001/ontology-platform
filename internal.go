package ontology

type inheritCounts struct {
	CheckedUsers       int
	CheckedSessions    int
	RecomputedUsers    int
	RecomputedSessions int
}

func (m *Manager) lastInheritCounts() inheritCounts {
	return inheritCounts{
		CheckedUsers:       m.inheritCheckedUsers,
		CheckedSessions:    m.inheritCheckedSessions,
		RecomputedUsers:    m.inheritRecomputedUsers,
		RecomputedSessions: m.inheritRecomputedSessions,
	}
}

func (m *Manager) resetInheritCounts() {
	m.inheritCheckedUsers = 0
	m.inheritCheckedSessions = 0
	m.inheritRecomputedUsers = 0
	m.inheritRecomputedSessions = 0
}

func (m *Manager) authForTest(user string) map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authRoles(user)
}

func (m *Manager) effectiveForTest(sid string) map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.effectiveRoles(m.sessions[sid])
}

func (m *Manager) activeForTest(sid string) map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := map[string]struct{}{}
	for role := range m.sessions[sid].active {
		result[role] = struct{}{}
	}
	return result
}

func (m *Manager) edgeExistsForTest(senior, junior string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return containsSet(m.juniorEdges[senior], junior)
}
