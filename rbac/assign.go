package rbac

// AssignUser directly assigns role to user. The new Auth(user) (union with
// Juniors(role)) is checked against every SSD constraint; any violation
// rejects the operation without side effects.
func (r *RBAC) AssignUser(user, role string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" || role == "" {
		return &Error{Cat: CatInvalidParam, Op: "AssignUser", Kind: "EmptyName",
			Object: user + "/" + role}
	}
	u, ok := r.users[user]
	if !ok {
		return &Error{Cat: CatNotExist, Op: "AssignUser", Kind: "UserNotExist", Object: user}
	}
	if _, ok := r.roles[role]; !ok {
		return &Error{Cat: CatNotExist, Op: "AssignUser", Kind: "RoleNotExist", Object: role}
	}
	if u.assigned[role] {
		return &Error{Cat: CatConflict, Op: "AssignUser", Kind: "DuplicateAssignment",
			Object: user + "/" + role}
	}
	gained := r.juniors(role)
	newAuth := map[string]bool{}
	unionInto(newAuth, u.auth)
	unionInto(newAuth, gained)
	if vs := r.ssdViolationsAgainst(newAuth); len(vs) > 0 {
		min := vs[0]
		for _, v := range vs[1:] {
			if v < min {
				min = v
			}
		}
		return &Error{Cat: CatViolation, Op: "AssignUser", Kind: "SSDViolation",
			Constraint: min, Subject: user}
	}
	u.assigned[role] = true
	for x := range gained {
		if !u.auth[x] {
			u.auth[x] = true
			idxAdd(r.authIdx, x, user)
		}
	}
	return nil
}

// DeassignUser removes a direct assignment, recomputes Auth(user), and
// cascades: in every session of the user, explicitly activated roles that no
// longer belong to the new Auth are deactivated (roles still reachable
// through other assigned roles survive). Returns the deactivated
// (session, role) pairs sorted by session id then role name.
func (r *RBAC) DeassignUser(user, role string) ([]Pair, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" || role == "" {
		return nil, &Error{Cat: CatInvalidParam, Op: "DeassignUser", Kind: "EmptyName",
			Object: user + "/" + role}
	}
	u, ok := r.users[user]
	if !ok {
		return nil, &Error{Cat: CatNotExist, Op: "DeassignUser", Kind: "UserNotExist", Object: user}
	}
	if _, ok := r.roles[role]; !ok {
		return nil, &Error{Cat: CatNotExist, Op: "DeassignUser", Kind: "RoleNotExist", Object: role}
	}
	if !u.assigned[role] {
		return nil, &Error{Cat: CatConflict, Op: "DeassignUser", Kind: "NotAssigned",
			Object: user + "/" + role}
	}
	oldAuth := u.auth
	delete(u.assigned, role)
	newAuth := r.computeAuth(u)

	// Roles that left Auth; only sessions explicitly activating one of them
	// can lose an explicit role.
	candidates := map[string]bool{}
	for x := range oldAuth {
		if !newAuth[x] {
			for sid := range r.explIdx[x] {
				if u.sessions[sid] {
					candidates[sid] = true
				}
			}
		}
	}
	r.setAuth(user, u, newAuth)
	return r.cascadeDeactivations(candidates), nil
}
