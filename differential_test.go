package ontology

type testOp struct {
	name  string
	a, b  string
	roles []string
	n     int
}

type snapshot struct {
	roles, users, perms map[string]struct{}
	edges               map[[2]string]struct{}
	assigned            map[string]map[string]struct{}
	active              map[string]map[string]struct{}
	sessionUsers        map[string]string
	rolePerms           map[string]map[string]struct{}
	ssd, dsd            map[string]map[string]struct{}
	ssdN, dsdN          map[string]int
}

func realSnapshot(m *Manager) snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := snapshot{
		roles:        map[string]struct{}{},
		users:        map[string]struct{}{},
		perms:        setCopy(m.perms),
		edges:        map[[2]string]struct{}{},
		assigned:     map[string]map[string]struct{}{},
		active:       map[string]map[string]struct{}{},
		sessionUsers: map[string]string{},
		rolePerms:    map[string]map[string]struct{}{},
		ssd:          map[string]map[string]struct{}{},
		dsd:          map[string]map[string]struct{}{},
		ssdN:         map[string]int{},
		dsdN:         map[string]int{},
	}
	for role := range m.roles {
		s.roles[role] = struct{}{}
	}
	for user := range m.users {
		s.users[user] = struct{}{}
		s.assigned[user] = setCopy(m.userRoles[user])
	}
	for senior, juniors := range m.juniorEdges {
		for junior := range juniors {
			s.edges[[2]string{senior, junior}] = struct{}{}
		}
	}
	for sid, sess := range m.sessions {
		s.sessionUsers[sid] = sess.user
		s.active[sid] = setCopy(sess.active)
	}
	for role, perms := range m.rolePerms {
		s.rolePerms[role] = setCopy(perms)
	}
	for name, c := range m.ssd {
		s.ssd[name] = setCopy(c.roles)
		s.ssdN[name] = c.n
	}
	for name, c := range m.dsd {
		s.dsd[name] = setCopy(c.roles)
		s.dsdN[name] = c.n
	}
	return s
}

func naiveSnapshot(n *naiveManager) snapshot {
	s := snapshot{
		roles:        setCopy(n.roles),
		users:        map[string]struct{}{},
		perms:        map[string]struct{}{},
		edges:        map[[2]string]struct{}{},
		assigned:     map[string]map[string]struct{}{},
		active:       map[string]map[string]struct{}{},
		sessionUsers: map[string]string{},
		rolePerms:    map[string]map[string]struct{}{},
		ssd:          map[string]map[string]struct{}{},
		dsd:          map[string]map[string]struct{}{},
		ssdN:         map[string]int{},
		dsdN:         map[string]int{},
	}
	for user, roles := range n.users {
		s.users[user] = struct{}{}
		s.assigned[user] = setCopy(roles)
	}
	for edge := range n.edges {
		s.edges[edge] = struct{}{}
	}
	for sid, sess := range n.sessions {
		s.sessionUsers[sid] = sess.user
		s.active[sid] = setCopy(sess.active)
	}
	for role, perms := range n.perms {
		s.rolePerms[role] = setCopy(perms)
		for perm := range perms {
			s.perms[perm] = struct{}{}
		}
	}
	for name, c := range n.ssd {
		s.ssd[name] = setCopy(c.roles)
		s.ssdN[name] = c.n
	}
	for name, c := range n.dsd {
		s.dsd[name] = setCopy(c.roles)
		s.dsdN[name] = c.n
	}
	return s
}

type opResult struct {
	err     string
	kind    ErrorKind
	removed []InactiveAssignment
	ok      bool
}

func executeReal(m *Manager, op testOp) opResult {
	r := opResult{}
	var err error
	switch op.name {
	case "AddRole":
		err = m.AddRole(op.a)
	case "AddUser":
		err = m.AddUser(op.a)
	case "AddInherit":
		err = m.AddInherit(op.a, op.b)
	case "DeleteInherit":
		r.removed, err = m.DeleteInherit(op.a, op.b)
	case "AssignUser":
		err = m.AssignUser(op.a, op.b)
	case "DeassignUser":
		r.removed, err = m.DeassignUser(op.a, op.b)
	case "AddSSD":
		err = m.AddSSD(op.a, op.roles, op.n)
	case "AddDSD":
		err = m.AddDSD(op.a, op.roles, op.n)
	case "CreateSession":
		err = m.CreateSession(op.a, op.b)
	case "DeleteSession":
		err = m.DeleteSession(op.a)
	case "Activate":
		err = m.Activate(op.a, op.b)
	case "Deactivate":
		err = m.Deactivate(op.a, op.b)
	case "AssignPerm":
		err = m.AssignPerm(op.a, op.b)
	case "Check":
		r.ok, err = m.Check(op.a, op.b)
	}
	if err != nil {
		e := err.(*RBACError)
		r.err = e.Error()
		r.kind = e.Kind
	}
	return r
}

func executeNaive(n *naiveManager, op testOp) opResult {
	r := opResult{}
	var err error
	switch op.name {
	case "AddRole":
		err = n.addRole(op.a)
	case "AddUser":
		err = n.addUser(op.a)
	case "AddInherit":
		err = n.addInherit(op.a, op.b)
	case "DeleteInherit":
		r.removed, err = n.deleteInherit(op.a, op.b)
	case "AssignUser":
		err = n.assign(op.a, op.b)
	case "DeassignUser":
		r.removed, err = n.deassign(op.a, op.b)
	case "AddSSD":
		err = n.addSSD(op.a, op.roles, op.n)
	case "AddDSD":
		err = n.addDSD(op.a, op.roles, op.n)
	case "CreateSession":
		err = n.createSession(op.a, op.b)
	case "DeleteSession":
		err = n.deleteSession(op.a)
	case "Activate":
		err = n.activate(op.a, op.b)
	case "Deactivate":
		err = n.deactivate(op.a, op.b)
	case "AssignPerm":
		err = n.assignPerm(op.a, op.b)
	case "Check":
		r.ok, err = n.check(op.a, op.b)
	}
	if err != nil {
		e := err.(*RBACError)
		r.err = e.Error()
		r.kind = e.Kind
	}
	return r
}
