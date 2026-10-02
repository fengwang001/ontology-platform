package ontology

func (m *Manager) AddSSD(name string, roles []string, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.prepareConstraint(name, roles, n)
	if err != nil {
		return err
	}
	if _, ok := m.ssd[name]; ok {
		return conflict("ssd", name, "already exists")
	}
	for _, user := range sortedSet(m.users) {
		if m.violatesConstraint(m.authRoles(user), c) {
			return violation("user", user, name)
		}
	}
	if len(m.ssd)+len(m.dsd) >= m.cmax {
		return limitExceeded("constraint", name, "constraint limit exceeded")
	}
	m.ssd[name] = c
	return nil
}

func (m *Manager) AddDSD(name string, roles []string, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.prepareConstraint(name, roles, n)
	if err != nil {
		return err
	}
	if _, ok := m.dsd[name]; ok {
		return conflict("dsd", name, "already exists")
	}
	for _, sid := range sortedSessions(m.sessions) {
		if m.violatesConstraint(m.effectiveRoles(m.sessions[sid]), c) {
			return violation("session", sid, name)
		}
	}
	if len(m.ssd)+len(m.dsd) >= m.cmax {
		return limitExceeded("constraint", name, "constraint limit exceeded")
	}
	m.dsd[name] = c
	return nil
}

func (m *Manager) prepareConstraint(name string, roles []string, n int) (*constraint, error) {
	if name == "" {
		return nil, invalid("constraint", name, "name must not be empty")
	}
	if len(roles) < 2 {
		return nil, invalid("constraint", name, "role set must contain at least two roles")
	}
	roleSet := map[string]struct{}{}
	for _, role := range roles {
		if role == "" {
			return nil, invalid("role", role, "name must not be empty")
		}
		if _, ok := roleSet[role]; ok {
			return nil, invalid("constraint", name, "roles must be distinct")
		}
		roleSet[role] = struct{}{}
	}
	if n < 2 || n > len(roles) {
		return nil, invalid("constraint", name, "n must be between 2 and the role set size")
	}
	for _, role := range roles {
		if !containsSet(m.roles, role) {
			return nil, notFound("role", role)
		}
	}
	return &constraint{name: name, roles: roleSet, n: n}, nil
}
