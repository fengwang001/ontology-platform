package ontology

import "sort"

func (m *Manager) juniors(role string) map[string]struct{} {
	result := map[string]struct{}{role: {}}
	stack := []string{role}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for junior := range m.juniorEdges[current] {
			if _, seen := result[junior]; !seen {
				result[junior] = struct{}{}
				stack = append(stack, junior)
			}
		}
	}
	return result
}

func (m *Manager) ancestors(role string) map[string]struct{} {
	result := map[string]struct{}{role: {}}
	stack := []string{role}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for senior := range m.seniorEdges[current] {
			if _, seen := result[senior]; !seen {
				result[senior] = struct{}{}
				stack = append(stack, senior)
			}
		}
	}
	return result
}

func (m *Manager) reaches(senior, junior string) bool {
	_, ok := m.juniors(senior)[junior]
	return ok
}

func (m *Manager) authRoles(user string) map[string]struct{} {
	result := map[string]struct{}{}
	for role := range m.userRoles[user] {
		unionInto(result, m.juniors(role))
	}
	return result
}

func (m *Manager) effectiveRoles(sess *session) map[string]struct{} {
	result := map[string]struct{}{}
	for role := range sess.active {
		unionInto(result, m.juniors(role))
	}
	return result
}

func unionInto(dst, src map[string]struct{}) {
	for value := range src {
		dst[value] = struct{}{}
	}
}

func containsSet(set map[string]struct{}, value string) bool {
	_, ok := set[value]
	return ok
}

func setCopy(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func sortedSet(set map[string]struct{}) []string {
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (m *Manager) violatesConstraint(roles map[string]struct{}, c *constraint) bool {
	count := 0
	for role := range c.roles {
		if _, ok := roles[role]; ok {
			count++
			if count >= c.n {
				return true
			}
		}
	}
	return false
}

func (m *Manager) ssdViolation(users []string, authByUser map[string]map[string]struct{}) *RBACError {
	names := sortedConstraints(m.ssd)
	users = append([]string(nil), users...)
	sort.Strings(users)
	for _, name := range names {
		c := m.ssd[name]
		for _, user := range users {
			auth := authByUser[user]
			if auth == nil {
				auth = m.authRoles(user)
			}
			if m.violatesConstraint(auth, c) {
				return violation("user", user, name)
			}
		}
	}
	return nil
}

func (m *Manager) dsdViolation(sessions []string, effBySession map[string]map[string]struct{}) *RBACError {
	names := sortedConstraints(m.dsd)
	sessions = append([]string(nil), sessions...)
	sort.Strings(sessions)
	for _, name := range names {
		c := m.dsd[name]
		for _, sid := range sessions {
			eff := effBySession[sid]
			if eff == nil {
				eff = m.effectiveRoles(m.sessions[sid])
			}
			if m.violatesConstraint(eff, c) {
				return violation("session", sid, name)
			}
		}
	}
	return nil
}

func sortedConstraints(constraints map[string]*constraint) []string {
	result := make([]string, 0, len(constraints))
	for name := range constraints {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (m *Manager) affectedUsers(role string) map[string]struct{} {
	result := map[string]struct{}{}
	for assignedRole := range m.ancestors(role) {
		for user := range m.roleUsers[assignedRole] {
			result[user] = struct{}{}
		}
	}
	return result
}

func (m *Manager) affectedSessions(role string) map[string]struct{} {
	result := map[string]struct{}{}
	for activeRole := range m.ancestors(role) {
		for sid := range m.activeRoleSessions[activeRole] {
			result[sid] = struct{}{}
		}
	}
	return result
}

func (m *Manager) affectedUserSessions(users map[string]struct{}) map[string]struct{} {
	result := map[string]struct{}{}
	for sid, sess := range m.sessions {
		if containsSet(users, sess.user) {
			result[sid] = struct{}{}
		}
	}
	return result
}
