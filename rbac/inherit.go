package rbac

import "sort"

// AddInherit adds an inheritance edge (senior, junior): holding senior then
// implies holding all of Juniors(junior). The edge is rejected, without any
// side effect, if it is a duplicate, would create a cycle, or would make any
// affected user violate an SSD constraint or any affected session violate a
// DSD constraint (SSD checked first).
func (r *RBAC) AddInherit(senior, junior string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastUsersChecked, r.lastSessionsChecked = 0, 0

	if senior == "" || junior == "" {
		return &Error{Cat: CatInvalidParam, Op: "AddInherit", Kind: "EmptyName",
			Object: senior + "/" + junior}
	}
	if _, ok := r.roles[senior]; !ok {
		return &Error{Cat: CatNotExist, Op: "AddInherit", Kind: "RoleNotExist", Object: senior}
	}
	if _, ok := r.roles[junior]; !ok {
		return &Error{Cat: CatNotExist, Op: "AddInherit", Kind: "RoleNotExist", Object: junior}
	}
	if r.edges[senior][junior] {
		return &Error{Cat: CatConflict, Op: "AddInherit", Kind: "DuplicateEdge",
			Object: senior + "/" + junior}
	}
	// Adding senior->junior creates a cycle iff junior already reaches
	// senior (which includes senior == junior, since Juniors(r) contains r).
	if r.juniors(junior)[senior] {
		return &Error{Cat: CatConflict, Op: "AddInherit", Kind: "Cycle",
			Object: senior + "/" + junior}
	}

	gained := r.juniors(junior)

	// SSD phase: every user whose Auth contains senior gains Juniors(junior).
	// All affected users are checked against all SSD constraints so the
	// byte-order-minimum violator can be reported.
	affectedUsers := r.authIdx[senior]
	r.lastUsersChecked = len(affectedUsers)
	var vs []violation
	for user := range affectedUsers {
		newAuth := map[string]bool{}
		unionInto(newAuth, r.users[user].auth)
		unionInto(newAuth, gained)
		for _, cname := range r.ssdViolationsAgainst(newAuth) {
			vs = append(vs, violation{constraint: cname, subject: user})
		}
	}
	if len(vs) > 0 {
		min := pickMin(vs)
		return &Error{Cat: CatViolation, Op: "AddInherit", Kind: "SSDViolation",
			Constraint: min.constraint, Subject: min.subject}
	}

	// DSD phase: only entered when no SSD violation was found.
	affectedSessions := r.effIdx[senior]
	r.lastSessionsChecked = len(affectedSessions)
	for sid := range affectedSessions {
		newEff := map[string]bool{}
		unionInto(newEff, r.sessions[sid].eff)
		unionInto(newEff, gained)
		for _, cname := range r.dsdViolationsAgainst(newEff) {
			vs = append(vs, violation{constraint: cname, subject: sid})
		}
	}
	if len(vs) > 0 {
		min := pickMin(vs)
		return &Error{Cat: CatViolation, Op: "AddInherit", Kind: "DSDViolation",
			Constraint: min.constraint, Subject: min.subject}
	}

	// Apply: add the edge and widen cached Auth/Eff plus the reverse indexes.
	if r.edges[senior] == nil {
		r.edges[senior] = map[string]bool{}
	}
	r.edges[senior][junior] = true
	for user := range affectedUsers {
		u := r.users[user]
		for role := range gained {
			if !u.auth[role] {
				u.auth[role] = true
				idxAdd(r.authIdx, role, user)
			}
		}
	}
	for sid := range affectedSessions {
		s := r.sessions[sid]
		for role := range gained {
			if !s.eff[role] {
				s.eff[role] = true
				idxAdd(r.effIdx, role, sid)
			}
		}
	}
	return nil
}

// DeleteInherit removes the edge (senior, junior), recomputes the Auth of
// every affected user, and cascades: for each affected session, explicitly
// activated roles no longer in the owner's new Auth are deactivated (roles
// still reachable through other paths survive). It returns the deactivated
// (session, role) pairs sorted by session id then role name.
func (r *RBAC) DeleteInherit(senior, junior string) ([]Pair, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastUsersChecked, r.lastSessionsChecked = 0, 0

	if senior == "" || junior == "" {
		return nil, &Error{Cat: CatInvalidParam, Op: "DeleteInherit", Kind: "EmptyName",
			Object: senior + "/" + junior}
	}
	if _, ok := r.roles[senior]; !ok {
		return nil, &Error{Cat: CatNotExist, Op: "DeleteInherit", Kind: "RoleNotExist", Object: senior}
	}
	if _, ok := r.roles[junior]; !ok {
		return nil, &Error{Cat: CatNotExist, Op: "DeleteInherit", Kind: "RoleNotExist", Object: junior}
	}
	if !r.edges[senior][junior] {
		return nil, &Error{Cat: CatConflict, Op: "DeleteInherit", Kind: "EdgeNotExist",
			Object: senior + "/" + junior}
	}

	// Snapshot before mutating: roles that may leave anyone's Auth are
	// exactly Juniors(junior) under the old graph.
	mayLeave := r.juniors(junior)
	affectedUsers := map[string]bool{}
	for user := range r.authIdx[senior] {
		affectedUsers[user] = true
	}
	effTouched := map[string]bool{}
	for sid := range r.effIdx[senior] {
		effTouched[sid] = true
	}
	r.lastUsersChecked = len(affectedUsers)

	// Apply the edge removal and recompute affected users' Auth.
	delete(r.edges[senior], junior)
	for user := range affectedUsers {
		u := r.users[user]
		r.setAuth(user, u, r.computeAuth(u))
	}

	// Candidate sessions for deactivation: owned by an affected user and
	// explicitly activating a role that may have left the owner's Auth.
	candidates := map[string]bool{}
	for role := range mayLeave {
		for sid := range r.explIdx[role] {
			if affectedUsers[r.sessions[sid].owner] {
				candidates[sid] = true
			}
		}
	}
	r.lastSessionsChecked = len(candidates)
	deactivated := r.cascadeDeactivations(candidates)

	// Sessions whose Eff changed only because Juniors of their explicit
	// roles shrank (no deactivation happened there).
	for sid := range effTouched {
		if !candidates[sid] {
			s := r.sessions[sid]
			r.setEff(sid, s, r.computeEff(s))
		}
	}
	return deactivated, nil
}

// cascadeDeactivations evaluates each candidate session: every explicitly
// activated role that no longer belongs to the owner's Auth is deactivated,
// and Eff is recomputed. Returns deactivated pairs sorted by session then role.
func (r *RBAC) cascadeDeactivations(candidates map[string]bool) []Pair {
	var out []Pair
	for _, sid := range sortedKeys(candidates) {
		s := r.sessions[sid]
		auth := r.users[s.owner].auth
		var dropped []string
		for role := range s.active {
			if !auth[role] {
				dropped = append(dropped, role)
			}
		}
		if len(dropped) == 0 {
			continue
		}
		sort.Strings(dropped)
		for _, role := range dropped {
			delete(s.active, role)
			idxDel(r.explIdx, role, sid)
			out = append(out, Pair{Session: sid, Role: role})
		}
		r.setEff(sid, s, r.computeEff(s))
	}
	return out
}
