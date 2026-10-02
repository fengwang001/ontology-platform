package ontology

func (m *Manager) AddInherit(senior, junior string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if senior == "" {
		return invalid("role", senior, "name must not be empty")
	}
	if junior == "" {
		return invalid("role", junior, "name must not be empty")
	}
	if !containsSet(m.roles, senior) {
		return notFound("role", senior)
	}
	if !containsSet(m.roles, junior) {
		return notFound("role", junior)
	}
	if containsSet(m.juniorEdges[senior], junior) {
		return conflict("inherit", senior+"->"+junior, "edge already exists")
	}
	if senior == junior || m.reaches(junior, senior) {
		return conflict("inherit", senior+"->"+junior, "would form a cycle")
	}

	m.inheritCheckedUsers, m.inheritCheckedSessions = 0, 0
	m.inheritRecomputedUsers, m.inheritRecomputedSessions = 0, 0
	userIDs := sortedSet(m.affectedUsers(senior))
	m.inheritCheckedUsers = len(userIDs)
	juniorClosure := m.juniors(junior)
	candidateAuth := make(map[string]map[string]struct{}, len(userIDs))
	for _, user := range userIDs {
		auth := m.authRoles(user)
		unionInto(auth, juniorClosure)
		candidateAuth[user] = auth
	}
	if err := m.ssdViolation(userIDs, candidateAuth); err != nil {
		return err
	}

	sessionIDs := sortedSet(m.affectedSessions(senior))
	m.inheritCheckedSessions = len(sessionIDs)
	candidateEff := make(map[string]map[string]struct{}, len(sessionIDs))
	for _, sid := range sessionIDs {
		eff := m.effectiveRoles(m.sessions[sid])
		unionInto(eff, juniorClosure)
		candidateEff[sid] = eff
	}
	if err := m.dsdViolation(sessionIDs, candidateEff); err != nil {
		return err
	}

	if m.juniorEdges[senior] == nil {
		m.juniorEdges[senior] = map[string]struct{}{}
	}
	if m.seniorEdges[junior] == nil {
		m.seniorEdges[junior] = map[string]struct{}{}
	}
	m.juniorEdges[senior][junior] = struct{}{}
	m.seniorEdges[junior][senior] = struct{}{}
	return nil
}

func (m *Manager) DeleteInherit(senior, junior string) ([]InactiveAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if senior == "" {
		return nil, invalid("role", senior, "name must not be empty")
	}
	if junior == "" {
		return nil, invalid("role", junior, "name must not be empty")
	}
	if !containsSet(m.roles, senior) {
		return nil, notFound("role", senior)
	}
	if !containsSet(m.roles, junior) {
		return nil, notFound("role", junior)
	}
	if !containsSet(m.juniorEdges[senior], junior) {
		return nil, conflict("inherit", senior+"->"+junior, "edge does not exist")
	}

	affectedUsers := m.affectedUsers(senior)
	affectedSessions := m.affectedUserSessions(affectedUsers)
	m.inheritCheckedUsers, m.inheritCheckedSessions = 0, 0
	m.inheritRecomputedUsers, m.inheritRecomputedSessions = len(affectedUsers), len(affectedSessions)
	delete(m.juniorEdges[senior], junior)
	delete(m.seniorEdges[junior], senior)

	newAuthByUser := make(map[string]map[string]struct{}, len(affectedUsers))
	for user := range affectedUsers {
		newAuthByUser[user] = m.authRoles(user)
	}
	var removed []InactiveAssignment
	for sid := range affectedSessions {
		sess := m.sessions[sid]
		newAuth := newAuthByUser[sess.user]
		for role := range sess.active {
			if !containsSet(newAuth, role) {
				removed = append(removed, InactiveAssignment{Session: sid, Role: role})
			}
		}
	}
	sortInactive(removed)
	for _, item := range removed {
		m.removeActive(item.Session, item.Role)
	}
	return removed, nil
}
