package ontology

import "sort"

func sortInactive(items []InactiveAssignment) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Session != items[j].Session {
			return items[i].Session < items[j].Session
		}
		return items[i].Role < items[j].Role
	})
}

func (m *Manager) deactivateUnauthorized(user string, newAuth map[string]struct{}) []InactiveAssignment {
	type pair struct {
		sid  string
		role string
	}
	var pairs []pair
	sessionIDs := make([]string, 0, len(m.sessions))
	for sid, sess := range m.sessions {
		if sess.user == user {
			sessionIDs = append(sessionIDs, sid)
		}
	}
	for _, sid := range sessionIDs {
		sess := m.sessions[sid]
		for role := range sess.active {
			if !containsSet(newAuth, role) {
				pairs = append(pairs, pair{sid: sid, role: role})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].sid != pairs[j].sid {
			return pairs[i].sid < pairs[j].sid
		}
		return pairs[i].role < pairs[j].role
	})
	result := make([]InactiveAssignment, 0, len(pairs))
	for _, p := range pairs {
		m.removeActive(p.sid, p.role)
		result = append(result, InactiveAssignment{Session: p.sid, Role: p.role})
	}
	return result
}

func (m *Manager) removeActive(sid, role string) {
	sess := m.sessions[sid]
	delete(sess.active, role)
	if byRole := m.activeRoleSessions[role]; byRole != nil {
		delete(byRole, sid)
	}
}

func (m *Manager) addActive(sid, role string) {
	sess := m.sessions[sid]
	sess.active[role] = struct{}{}
	if m.activeRoleSessions[role] == nil {
		m.activeRoleSessions[role] = map[string]struct{}{}
	}
	m.activeRoleSessions[role][sid] = struct{}{}
}
