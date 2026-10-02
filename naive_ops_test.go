package ontology

import "fmt"

func (n *naiveManager) addRole(role string) error {
	if role == "" {
		return invalid("role", role, "empty")
	}
	if _, ok := n.roles[role]; ok {
		return conflict("role", role, "exists")
	}
	n.roles[role] = struct{}{}
	return nil
}

func (n *naiveManager) addUser(user string) error {
	if user == "" {
		return invalid("user", user, "empty")
	}
	if _, ok := n.users[user]; ok {
		return conflict("user", user, "exists")
	}
	n.users[user] = map[string]struct{}{}
	return nil
}

func (n *naiveManager) addInherit(senior, junior string) error {
	if senior == "" {
		return invalid("role", senior, "empty")
	}
	if junior == "" {
		return invalid("role", junior, "empty")
	}
	if _, ok := n.roles[senior]; !ok {
		return notFound("role", senior)
	}
	if _, ok := n.roles[junior]; !ok {
		return notFound("role", junior)
	}
	edge := [2]string{senior, junior}
	if _, ok := n.edges[edge]; ok {
		return conflict("inherit", fmt.Sprint(edge), "exists")
	}
	if senior == junior || n.reaches(junior, senior) {
		return conflict("inherit", fmt.Sprint(edge), "cycle")
	}
	n.checkUsers, n.checkSess, n.recompUsers, n.recompSess = 0, 0, 0, 0
	var users []string
	auths := map[string]map[string]struct{}{}
	for user := range n.users {
		if _, ok := n.auth(user)[senior]; ok {
			users = append(users, user)
		}
	}
	n.checkUsers = len(users)
	for _, user := range users {
		candidate := n.auth(user)
		unionInto(candidate, n.juniors(junior))
		auths[user] = candidate
	}
	if err := n.ssdError(users, auths); err != nil {
		return err
	}
	var sids []string
	effs := map[string]map[string]struct{}{}
	for sid, sess := range n.sessions {
		if _, ok := n.eff(sess)[senior]; ok {
			sids = append(sids, sid)
		}
	}
	n.checkSess = len(sids)
	for _, sid := range sids {
		candidate := n.eff(n.sessions[sid])
		unionInto(candidate, n.juniors(junior))
		effs[sid] = candidate
	}
	if err := n.dsdError(sids, effs); err != nil {
		return err
	}
	n.edges[edge] = struct{}{}
	return nil
}

func (n *naiveManager) deleteInherit(senior, junior string) ([]InactiveAssignment, error) {
	if senior == "" {
		return nil, invalid("role", senior, "empty")
	}
	if junior == "" {
		return nil, invalid("role", junior, "empty")
	}
	if _, ok := n.roles[senior]; !ok {
		return nil, notFound("role", senior)
	}
	if _, ok := n.roles[junior]; !ok {
		return nil, notFound("role", junior)
	}
	edge := [2]string{senior, junior}
	if _, ok := n.edges[edge]; !ok {
		return nil, conflict("inherit", fmt.Sprint(edge), "missing")
	}
	users := map[string]struct{}{}
	for user := range n.users {
		if _, ok := n.auth(user)[senior]; ok {
			users[user] = struct{}{}
		}
	}
	sids := map[string]struct{}{}
	for sid, sess := range n.sessions {
		if _, ok := users[sess.user]; ok {
			sids[sid] = struct{}{}
		}
	}
	n.checkUsers, n.checkSess = 0, 0
	n.recompUsers, n.recompSess = len(users), len(sids)
	delete(n.edges, edge)
	auths := map[string]map[string]struct{}{}
	for user := range users {
		auths[user] = n.auth(user)
	}
	var removed []InactiveAssignment
	for sid := range sids {
		for role := range n.sessions[sid].active {
			if _, ok := auths[n.sessions[sid].user][role]; !ok {
				removed = append(removed, InactiveAssignment{Session: sid, Role: role})
			}
		}
	}
	sortInactive(removed)
	for _, item := range removed {
		delete(n.sessions[item.Session].active, item.Role)
	}
	return removed, nil
}
