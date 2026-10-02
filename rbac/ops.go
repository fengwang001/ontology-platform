package rbac

// AddRole registers a role with an empty permission set.
func (r *RBAC) AddRole(role string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if role == "" {
		return &Error{Cat: CatInvalidParam, Op: "AddRole", Kind: "EmptyName", Object: role}
	}
	if _, ok := r.roles[role]; ok {
		return &Error{Cat: CatConflict, Op: "AddRole", Kind: "DuplicateRole", Object: role}
	}
	r.roles[role] = map[string]bool{}
	return nil
}

// AddUser registers a user with no assigned roles.
func (r *RBAC) AddUser(user string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if user == "" {
		return &Error{Cat: CatInvalidParam, Op: "AddUser", Kind: "EmptyName", Object: user}
	}
	if _, ok := r.users[user]; ok {
		return &Error{Cat: CatConflict, Op: "AddUser", Kind: "DuplicateUser", Object: user}
	}
	r.users[user] = &userRec{
		assigned: map[string]bool{},
		auth:     map[string]bool{},
		sessions: map[string]bool{},
	}
	return nil
}

// AssignPerm grants a permission to a role.
func (r *RBAC) AssignPerm(role, perm string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if role == "" || perm == "" {
		return &Error{Cat: CatInvalidParam, Op: "AssignPerm", Kind: "EmptyName",
			Object: role + "/" + perm}
	}
	perms, ok := r.roles[role]
	if !ok {
		return &Error{Cat: CatNotExist, Op: "AssignPerm", Kind: "RoleNotExist", Object: role}
	}
	if perms[perm] {
		return &Error{Cat: CatConflict, Op: "AssignPerm", Kind: "DuplicatePerm", Object: perm}
	}
	perms[perm] = true
	return nil
}

// CreateSession opens a session owned by user, honoring the per-user limit.
func (r *RBAC) CreateSession(sid, user string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sid == "" || user == "" {
		return &Error{Cat: CatInvalidParam, Op: "CreateSession", Kind: "EmptyName",
			Object: sid + "/" + user}
	}
	u, ok := r.users[user]
	if !ok {
		return &Error{Cat: CatNotExist, Op: "CreateSession", Kind: "UserNotExist", Object: user}
	}
	if _, ok := r.sessions[sid]; ok {
		return &Error{Cat: CatConflict, Op: "CreateSession", Kind: "DuplicateSession", Object: sid}
	}
	if len(u.sessions) >= r.smax {
		return &Error{Cat: CatLimit, Op: "CreateSession", Kind: "SessionLimit", Object: user}
	}
	r.sessions[sid] = &sessionRec{
		owner:  user,
		active: map[string]bool{},
		eff:    map[string]bool{},
	}
	u.sessions[sid] = true
	return nil
}

// DeleteSession removes a session and all of its activations.
func (r *RBAC) DeleteSession(sid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sid == "" {
		return &Error{Cat: CatInvalidParam, Op: "DeleteSession", Kind: "EmptyName", Object: sid}
	}
	s, ok := r.sessions[sid]
	if !ok {
		return &Error{Cat: CatNotExist, Op: "DeleteSession", Kind: "SessionNotExist", Object: sid}
	}
	for role := range s.active {
		idxDel(r.explIdx, role, sid)
	}
	for role := range s.eff {
		idxDel(r.effIdx, role, sid)
	}
	delete(r.users[s.owner].sessions, sid)
	delete(r.sessions, sid)
	return nil
}

// Check reports whether perm is granted to some role in Eff(sid). It observes
// a consistent snapshot of the state. A missing session yields false.
func (r *RBAC) Check(sid, perm string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[sid]
	if !ok {
		return false
	}
	for role := range s.eff {
		if r.roles[role][perm] {
			return true
		}
	}
	return false
}
