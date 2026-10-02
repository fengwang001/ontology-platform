package ontology

func (n *naiveManager) assign(user, role string) error {
	if user == "" {
		return invalid("user", user, "empty")
	}
	if role == "" {
		return invalid("role", role, "empty")
	}
	if _, ok := n.users[user]; !ok {
		return notFound("user", user)
	}
	if _, ok := n.roles[role]; !ok {
		return notFound("role", role)
	}
	if _, ok := n.users[user][role]; ok {
		return conflict("assignment", user+role, "exists")
	}
	candidate := n.auth(user)
	unionInto(candidate, n.juniors(role))
	if err := n.ssdError([]string{user}, map[string]map[string]struct{}{user: candidate}); err != nil {
		return err
	}
	n.users[user][role] = struct{}{}
	return nil
}

func (n *naiveManager) deassign(user, role string) ([]InactiveAssignment, error) {
	if user == "" {
		return nil, invalid("user", user, "empty")
	}
	if role == "" {
		return nil, invalid("role", role, "empty")
	}
	if _, ok := n.users[user]; !ok {
		return nil, notFound("user", user)
	}
	if _, ok := n.roles[role]; !ok {
		return nil, notFound("role", role)
	}
	if _, ok := n.users[user][role]; !ok {
		return nil, conflict("assignment", user+role, "missing")
	}
	delete(n.users[user], role)
	newAuth := n.auth(user)
	var removed []InactiveAssignment
	for sid, sess := range n.sessions {
		if sess.user == user {
			for role := range sess.active {
				if _, ok := newAuth[role]; !ok {
					removed = append(removed, InactiveAssignment{Session: sid, Role: role})
				}
			}
		}
	}
	sortInactive(removed)
	for _, item := range removed {
		delete(n.sessions[item.Session].active, item.Role)
	}
	return removed, nil
}

func (n *naiveManager) prepareConstraint(name string, roles []string, k int) (naiveConstraint, error) {
	if name == "" {
		return naiveConstraint{}, invalid("constraint", name, "empty")
	}
	if len(roles) < 2 {
		return naiveConstraint{}, invalid("constraint", name, "too few roles")
	}
	set := map[string]struct{}{}
	for _, role := range roles {
		if role == "" {
			return naiveConstraint{}, invalid("role", role, "empty")
		}
		if _, ok := set[role]; ok {
			return naiveConstraint{}, invalid("constraint", name, "duplicate role")
		}
		set[role] = struct{}{}
	}
	if k < 2 || k > len(roles) {
		return naiveConstraint{}, invalid("constraint", name, "bad n")
	}
	for _, role := range roles {
		if _, ok := n.roles[role]; !ok {
			return naiveConstraint{}, notFound("role", role)
		}
	}
	return naiveConstraint{roles: set, n: k}, nil
}

func (n *naiveManager) addSSD(name string, roles []string, k int) error {
	c, err := n.prepareConstraint(name, roles, k)
	if err != nil {
		return err
	}
	if _, ok := n.ssd[name]; ok {
		return conflict("ssd", name, "exists")
	}
	for _, user := range sortedNaive(n.users) {
		if n.violated(n.auth(user), c) {
			return violation("user", user, name)
		}
	}
	if len(n.ssd)+len(n.dsd) >= n.cmax {
		return limitExceeded("constraint", name, "limit")
	}
	n.ssd[name] = c
	return nil
}

func (n *naiveManager) addDSD(name string, roles []string, k int) error {
	c, err := n.prepareConstraint(name, roles, k)
	if err != nil {
		return err
	}
	if _, ok := n.dsd[name]; ok {
		return conflict("dsd", name, "exists")
	}
	for _, sid := range sortedNaive(n.sessions) {
		if n.violated(n.eff(n.sessions[sid]), c) {
			return violation("session", sid, name)
		}
	}
	if len(n.ssd)+len(n.dsd) >= n.cmax {
		return limitExceeded("constraint", name, "limit")
	}
	n.dsd[name] = c
	return nil
}
