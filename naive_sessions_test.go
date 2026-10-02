package ontology

func (n *naiveManager) createSession(sid, user string) error {
	if sid == "" {
		return invalid("session", sid, "empty")
	}
	if user == "" {
		return invalid("user", user, "empty")
	}
	if _, ok := n.sessions[sid]; ok {
		return conflict("session", sid, "exists")
	}
	if _, ok := n.users[user]; !ok {
		return notFound("user", user)
	}
	count := 0
	for _, sess := range n.sessions {
		if sess.user == user {
			count++
		}
	}
	if count >= n.smax {
		return limitExceeded("session", sid, "limit")
	}
	n.sessions[sid] = &naiveSession{user: user, active: map[string]struct{}{}}
	return nil
}

func (n *naiveManager) deleteSession(sid string) error {
	if sid == "" {
		return invalid("session", sid, "empty")
	}
	if _, ok := n.sessions[sid]; !ok {
		return notFound("session", sid)
	}
	delete(n.sessions, sid)
	return nil
}

func (n *naiveManager) activate(sid, role string) error {
	if sid == "" {
		return invalid("session", sid, "empty")
	}
	if role == "" {
		return invalid("role", role, "empty")
	}
	sess, ok := n.sessions[sid]
	if !ok {
		return notFound("session", sid)
	}
	if _, ok := n.roles[role]; !ok {
		return notFound("role", role)
	}
	if _, ok := sess.active[role]; ok {
		return conflict("activation", sid+role, "duplicate")
	}
	if _, ok := n.auth(sess.user)[role]; !ok {
		return conflict("activation", sid+role, "unauthorized")
	}
	candidate := n.eff(sess)
	unionInto(candidate, n.juniors(role))
	if err := n.dsdError([]string{sid}, map[string]map[string]struct{}{sid: candidate}); err != nil {
		return err
	}
	sess.active[role] = struct{}{}
	return nil
}

func (n *naiveManager) deactivate(sid, role string) error {
	if sid == "" {
		return invalid("session", sid, "empty")
	}
	if role == "" {
		return invalid("role", role, "empty")
	}
	sess, ok := n.sessions[sid]
	if !ok {
		return notFound("session", sid)
	}
	if _, ok := n.roles[role]; !ok {
		return notFound("role", role)
	}
	if _, ok := sess.active[role]; ok {
		delete(sess.active, role)
		return nil
	}
	var by []string
	for _, active := range sortedNaive(sess.active) {
		if _, ok := n.juniors(active)[role]; ok {
			by = append(by, active)
		}
	}
	if len(by) > 0 {
		return conflict("role", role, "implicit", by...)
	}
	return conflict("role", role, "inactive")
}

func (n *naiveManager) assignPerm(role, perm string) error {
	if role == "" {
		return invalid("role", role, "empty")
	}
	if perm == "" {
		return invalid("permission", perm, "empty")
	}
	if _, ok := n.roles[role]; !ok {
		return notFound("role", role)
	}
	if n.perms[role] == nil {
		n.perms[role] = map[string]struct{}{}
	}
	if _, ok := n.perms[role][perm]; ok {
		return conflict("permission", perm, "exists")
	}
	n.perms[role][perm] = struct{}{}
	return nil
}

func (n *naiveManager) check(sid, perm string) (bool, error) {
	if sid == "" {
		return false, invalid("session", sid, "empty")
	}
	if perm == "" {
		return false, invalid("permission", perm, "empty")
	}
	sess, ok := n.sessions[sid]
	if !ok {
		return false, notFound("session", sid)
	}
	found := false
	for _, byRole := range n.perms {
		if _, ok := byRole[perm]; ok {
			found = true
		}
	}
	if !found {
		return false, notFound("permission", perm)
	}
	for role := range n.eff(sess) {
		if _, ok := n.perms[role][perm]; ok {
			return true, nil
		}
	}
	return false, nil
}
