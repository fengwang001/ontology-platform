package rbac

import "sort"

// Activate explicitly activates role in session sid. The role must belong to
// Auth(owner) and must not be explicitly activated already (a role that is
// merely implied by another explicit role may still be explicitly activated,
// which leaves Eff unchanged). The new Eff is checked against every DSD
// constraint; any violation rejects the operation without side effects.
func (r *RBAC) Activate(sid, role string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sid == "" || role == "" {
		return &Error{Cat: CatInvalidParam, Op: "Activate", Kind: "EmptyName",
			Object: sid + "/" + role}
	}
	s, ok := r.sessions[sid]
	if !ok {
		return &Error{Cat: CatNotExist, Op: "Activate", Kind: "SessionNotExist", Object: sid}
	}
	if _, ok := r.roles[role]; !ok {
		return &Error{Cat: CatNotExist, Op: "Activate", Kind: "RoleNotExist", Object: role}
	}
	if s.active[role] {
		return &Error{Cat: CatConflict, Op: "Activate", Kind: "DuplicateActivation",
			Object: sid + "/" + role}
	}
	if !r.users[s.owner].auth[role] {
		return &Error{Cat: CatConflict, Op: "Activate", Kind: "UnauthorizedActivation",
			Object: sid + "/" + role}
	}
	gained := r.juniors(role)
	newEff := map[string]bool{}
	unionInto(newEff, s.eff)
	unionInto(newEff, gained)
	if vs := r.dsdViolationsAgainst(newEff); len(vs) > 0 {
		min := vs[0]
		for _, v := range vs[1:] {
			if v < min {
				min = v
			}
		}
		return &Error{Cat: CatViolation, Op: "Activate", Kind: "DSDViolation",
			Constraint: min, Subject: sid}
	}
	s.active[role] = true
	idxAdd(r.explIdx, role, sid)
	for x := range gained {
		if !s.eff[x] {
			s.eff[x] = true
			idxAdd(r.effIdx, x, sid)
		}
	}
	return nil
}

// Deactivate removes an explicitly activated role from a session. A role
// that is only implicitly active (implied by other explicit roles) is
// rejected with the sorted list of implying explicit roles; a role that is
// not active at all is rejected as not activated.
func (r *RBAC) Deactivate(sid, role string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sid == "" || role == "" {
		return &Error{Cat: CatInvalidParam, Op: "Deactivate", Kind: "EmptyName",
			Object: sid + "/" + role}
	}
	s, ok := r.sessions[sid]
	if !ok {
		return &Error{Cat: CatNotExist, Op: "Deactivate", Kind: "SessionNotExist", Object: sid}
	}
	if _, ok := r.roles[role]; !ok {
		return &Error{Cat: CatNotExist, Op: "Deactivate", Kind: "RoleNotExist", Object: role}
	}
	if s.active[role] {
		delete(s.active, role)
		idxDel(r.explIdx, role, sid)
		r.setEff(sid, s, r.computeEff(s))
		return nil
	}
	if s.eff[role] {
		var impliers []string
		for a := range s.active {
			if r.juniors(a)[role] {
				impliers = append(impliers, a)
			}
		}
		sort.Strings(impliers)
		return &Error{Cat: CatConflict, Op: "Deactivate", Kind: "ImplicitActivation",
			Object: role, Impliers: impliers}
	}
	return &Error{Cat: CatConflict, Op: "Deactivate", Kind: "NotActivated", Object: role}
}
