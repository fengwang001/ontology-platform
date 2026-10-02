package ontology

func (m *Manager) AssignUser(user, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user == "" {
		return invalid("user", user, "name must not be empty")
	}
	if role == "" {
		return invalid("role", role, "name must not be empty")
	}
	if !containsSet(m.users, user) {
		return notFound("user", user)
	}
	if !containsSet(m.roles, role) {
		return notFound("role", role)
	}
	if containsSet(m.userRoles[user], role) {
		return conflict("assignment", user+"->"+role, "already assigned")
	}
	candidate := m.authRoles(user)
	unionInto(candidate, m.juniors(role))
	if err := m.ssdViolation([]string{user}, map[string]map[string]struct{}{user: candidate}); err != nil {
		return err
	}
	m.userRoles[user][role] = struct{}{}
	if m.roleUsers[role] == nil {
		m.roleUsers[role] = map[string]struct{}{}
	}
	m.roleUsers[role][user] = struct{}{}
	return nil
}

func (m *Manager) DeassignUser(user, role string) ([]InactiveAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user == "" {
		return nil, invalid("user", user, "name must not be empty")
	}
	if role == "" {
		return nil, invalid("role", role, "name must not be empty")
	}
	if !containsSet(m.users, user) {
		return nil, notFound("user", user)
	}
	if !containsSet(m.roles, role) {
		return nil, notFound("role", role)
	}
	if !containsSet(m.userRoles[user], role) {
		return nil, conflict("assignment", user+"->"+role, "not assigned")
	}
	delete(m.userRoles[user], role)
	delete(m.roleUsers[role], user)
	removed := m.deactivateUnauthorized(user, m.authRoles(user))
	return removed, nil
}
