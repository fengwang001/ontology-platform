package rbac

// AddSSD registers an SSD constraint (name, RS, n): every user's Auth must
// intersect RS in fewer than n roles. The new constraint is checked against
// all existing users; if any already violates it, the constraint is rejected
// and not registered. SSD and DSD constraints use independent namespaces.
func (r *RBAC) AddSSD(name string, rs []string, n int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	set, err := r.validateConstraint("AddSSD", name, rs, n)
	if err != nil {
		return err
	}
	if _, ok := r.ssd[name]; ok {
		return &Error{Cat: CatConflict, Op: "AddSSD", Kind: "DuplicateConstraint", Object: name}
	}
	// Check the new constraint against all existing users.
	minUser := ""
	for user, u := range r.users {
		if intersectCount(u.auth, set) >= n && (minUser == "" || user < minUser) {
			minUser = user
		}
	}
	if minUser != "" {
		return &Error{Cat: CatViolation, Op: "AddSSD", Kind: "SSDViolation",
			Constraint: name, Subject: minUser}
	}
	if r.ncons >= r.cmax {
		return &Error{Cat: CatLimit, Op: "AddSSD", Kind: "ConstraintLimit", Object: name}
	}
	r.ssd[name] = &constraint{name: name, rs: set, n: n}
	r.ncons++
	return nil
}

// AddDSD registers a DSD constraint (name, RS, n): every session's Eff must
// intersect RS in fewer than n roles. The new constraint is checked against
// all existing sessions; if any already violates it, the constraint is
// rejected and not registered.
func (r *RBAC) AddDSD(name string, rs []string, n int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	set, err := r.validateConstraint("AddDSD", name, rs, n)
	if err != nil {
		return err
	}
	if _, ok := r.dsd[name]; ok {
		return &Error{Cat: CatConflict, Op: "AddDSD", Kind: "DuplicateConstraint", Object: name}
	}
	// Check the new constraint against all existing sessions.
	minSid := ""
	for sid, s := range r.sessions {
		if intersectCount(s.eff, set) >= n && (minSid == "" || sid < minSid) {
			minSid = sid
		}
	}
	if minSid != "" {
		return &Error{Cat: CatViolation, Op: "AddDSD", Kind: "DSDViolation",
			Constraint: name, Subject: minSid}
	}
	if r.ncons >= r.cmax {
		return &Error{Cat: CatLimit, Op: "AddDSD", Kind: "ConstraintLimit", Object: name}
	}
	r.dsd[name] = &constraint{name: name, rs: set, n: n}
	r.ncons++
	return nil
}

// validateConstraint performs the InvalidParam and NotExist checks shared by
// AddSSD and AddDSD, and returns RS as a set.
func (r *RBAC) validateConstraint(op, name string, rs []string, n int) (map[string]bool, error) {
	if name == "" {
		return nil, &Error{Cat: CatInvalidParam, Op: op, Kind: "EmptyName", Object: name}
	}
	if len(rs) < 2 {
		return nil, &Error{Cat: CatInvalidParam, Op: op, Kind: "BadRS", Object: name}
	}
	set := map[string]bool{}
	for _, role := range rs {
		if role == "" {
			return nil, &Error{Cat: CatInvalidParam, Op: op, Kind: "EmptyName", Object: name}
		}
		if set[role] {
			return nil, &Error{Cat: CatInvalidParam, Op: op, Kind: "DuplicateRoleInRS",
				Object: role}
		}
		set[role] = true
	}
	if n < 2 || n > len(rs) {
		return nil, &Error{Cat: CatInvalidParam, Op: op, Kind: "BadN", Object: name}
	}
	// Roles are checked in the order given by RS; the first missing one is
	// reported.
	for _, role := range rs {
		if _, ok := r.roles[role]; !ok {
			return nil, &Error{Cat: CatNotExist, Op: op, Kind: "RoleNotExist", Object: role}
		}
	}
	return set, nil
}
