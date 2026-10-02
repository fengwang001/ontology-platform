package ontology

import "sort"

func (m *Manager) CreateSession(sid, user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid == "" {
		return invalid("session", sid, "name must not be empty")
	}
	if user == "" {
		return invalid("user", user, "name must not be empty")
	}
	if _, ok := m.sessions[sid]; ok {
		return conflict("session", sid, "already exists")
	}
	if !containsSet(m.users, user) {
		return notFound("user", user)
	}
	count := 0
	for _, sess := range m.sessions {
		if sess.user == user {
			count++
		}
	}
	if count >= m.smax {
		return limitExceeded("session", sid, "session limit exceeded")
	}
	m.sessions[sid] = &session{user: user, active: map[string]struct{}{}}
	return nil
}

func (m *Manager) DeleteSession(sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid == "" {
		return invalid("session", sid, "name must not be empty")
	}
	sess, ok := m.sessions[sid]
	if !ok {
		return notFound("session", sid)
	}
	for role := range sess.active {
		delete(m.activeRoleSessions[role], sid)
	}
	delete(m.sessions, sid)
	return nil
}

func (m *Manager) Activate(sid, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid == "" {
		return invalid("session", sid, "name must not be empty")
	}
	if role == "" {
		return invalid("role", role, "name must not be empty")
	}
	sess, ok := m.sessions[sid]
	if !ok {
		return notFound("session", sid)
	}
	if !containsSet(m.roles, role) {
		return notFound("role", role)
	}
	if containsSet(sess.active, role) {
		return conflict("activation", sid+"->"+role, "already explicitly active")
	}
	if !containsSet(m.authRoles(sess.user), role) {
		return conflict("activation", sid+"->"+role, "role is not authorized")
	}
	candidate := m.effectiveRoles(sess)
	unionInto(candidate, m.juniors(role))
	if err := m.dsdViolation([]string{sid}, map[string]map[string]struct{}{sid: candidate}); err != nil {
		return err
	}
	m.addActive(sid, role)
	return nil
}

func (m *Manager) Deactivate(sid, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid == "" {
		return invalid("session", sid, "name must not be empty")
	}
	if role == "" {
		return invalid("role", role, "name must not be empty")
	}
	sess, ok := m.sessions[sid]
	if !ok {
		return notFound("session", sid)
	}
	if !containsSet(m.roles, role) {
		return notFound("role", role)
	}
	if containsSet(sess.active, role) {
		m.removeActive(sid, role)
		return nil
	}
	var implicitBy []string
	for _, active := range sortedSet(sess.active) {
		if containsSet(m.juniors(active), role) {
			implicitBy = append(implicitBy, active)
		}
	}
	sort.Strings(implicitBy)
	if len(implicitBy) > 0 {
		return conflict("role", role, "implicitly active", implicitBy...)
	}
	return conflict("role", role, "not active")
}

func sortedSessions(sessions map[string]*session) []string {
	result := make([]string, 0, len(sessions))
	for sid := range sessions {
		result = append(result, sid)
	}
	sort.Strings(result)
	return result
}
