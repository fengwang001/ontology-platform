package rbac

// naive is an independent, deliberately simple implementation of the same
// specification, written directly from the rules: on every operation it
// recomputes Juniors/Auth/Eff from scratch and scans all users, sessions
// and constraints. The randomized differential test replays identical
// operation sequences against RBAC and naive and expects identical results.

import "sort"

type nConstraint struct {
	rs map[string]bool
	n  int
}

type naiveSession struct {
	owner  string
	active map[string]bool
}

type naive struct {
	smax, cmax int
	roles      map[string]map[string]bool
	users      map[string]map[string]bool // user -> directly assigned roles
	edges      map[string]map[string]bool
	ssd        map[string]nConstraint
	dsd        map[string]nConstraint
	sessions   map[string]*naiveSession

	lastUsers    int
	lastSessions int
}

func newNaive(smax, cmax int) *naive {
	return &naive{
		smax:     smax,
		cmax:     cmax,
		roles:    map[string]map[string]bool{},
		users:    map[string]map[string]bool{},
		edges:    map[string]map[string]bool{},
		ssd:      map[string]nConstraint{},
		dsd:      map[string]nConstraint{},
		sessions: map[string]*naiveSession{},
	}
}

func (n *naive) juniors(role string) map[string]bool {
	seen := map[string]bool{role: true}
	stack := []string{role}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for j := range n.edges[x] {
			if !seen[j] {
				seen[j] = true
				stack = append(stack, j)
			}
		}
	}
	return seen
}

func (n *naive) auth(user string) map[string]bool {
	out := map[string]bool{}
	for a := range n.users[user] {
		unionInto(out, n.juniors(a))
	}
	return out
}

func (n *naive) eff(sid string) map[string]bool {
	out := map[string]bool{}
	for a := range n.sessions[sid].active {
		unionInto(out, n.juniors(a))
	}
	return out
}

func nErr(cat Category, op, kind, object string) *Error {
	return &Error{Cat: cat, Op: op, Kind: kind, Object: object}
}

func (n *naive) AddRole(role string) error {
	if role == "" {
		return nErr(CatInvalidParam, "AddRole", "EmptyName", role)
	}
	if _, ok := n.roles[role]; ok {
		return nErr(CatConflict, "AddRole", "DuplicateRole", role)
	}
	n.roles[role] = map[string]bool{}
	return nil
}

func (n *naive) AddUser(user string) error {
	if user == "" {
		return nErr(CatInvalidParam, "AddUser", "EmptyName", user)
	}
	if _, ok := n.users[user]; ok {
		return nErr(CatConflict, "AddUser", "DuplicateUser", user)
	}
	n.users[user] = map[string]bool{}
	return nil
}

func (n *naive) AssignPerm(role, perm string) error {
	if role == "" || perm == "" {
		return nErr(CatInvalidParam, "AssignPerm", "EmptyName", role+"/"+perm)
	}
	perms, ok := n.roles[role]
	if !ok {
		return nErr(CatNotExist, "AssignPerm", "RoleNotExist", role)
	}
	if perms[perm] {
		return nErr(CatConflict, "AssignPerm", "DuplicatePerm", perm)
	}
	perms[perm] = true
	return nil
}

func (n *naive) AddInherit(senior, junior string) error {
	n.lastUsers, n.lastSessions = 0, 0
	if senior == "" || junior == "" {
		return nErr(CatInvalidParam, "AddInherit", "EmptyName", senior+"/"+junior)
	}
	if _, ok := n.roles[senior]; !ok {
		return nErr(CatNotExist, "AddInherit", "RoleNotExist", senior)
	}
	if _, ok := n.roles[junior]; !ok {
		return nErr(CatNotExist, "AddInherit", "RoleNotExist", junior)
	}
	if n.edges[senior][junior] {
		return nErr(CatConflict, "AddInherit", "DuplicateEdge", senior+"/"+junior)
	}
	if n.juniors(junior)[senior] {
		return nErr(CatConflict, "AddInherit", "Cycle", senior+"/"+junior)
	}
	gained := n.juniors(junior)
	var vs []violation
	for user := range n.users {
		a := n.auth(user)
		if !a[senior] {
			continue
		}
		n.lastUsers++
		newAuth := map[string]bool{}
		unionInto(newAuth, a)
		unionInto(newAuth, gained)
		for cname, c := range n.ssd {
			if intersectCount(newAuth, c.rs) >= c.n {
				vs = append(vs, violation{constraint: cname, subject: user})
			}
		}
	}
	if len(vs) > 0 {
		min := pickMin(vs)
		return &Error{Cat: CatViolation, Op: "AddInherit", Kind: "SSDViolation",
			Constraint: min.constraint, Subject: min.subject}
	}
	for sid := range n.sessions {
		e := n.eff(sid)
		if !e[senior] {
			continue
		}
		n.lastSessions++
		newEff := map[string]bool{}
		unionInto(newEff, e)
		unionInto(newEff, gained)
		for cname, c := range n.dsd {
			if intersectCount(newEff, c.rs) >= c.n {
				vs = append(vs, violation{constraint: cname, subject: sid})
			}
		}
	}
	if len(vs) > 0 {
		min := pickMin(vs)
		return &Error{Cat: CatViolation, Op: "AddInherit", Kind: "DSDViolation",
			Constraint: min.constraint, Subject: min.subject}
	}
	if n.edges[senior] == nil {
		n.edges[senior] = map[string]bool{}
	}
	n.edges[senior][junior] = true
	return nil
}

func (n *naive) DeleteInherit(senior, junior string) ([]Pair, error) {
	n.lastUsers, n.lastSessions = 0, 0
	if senior == "" || junior == "" {
		return nil, nErr(CatInvalidParam, "DeleteInherit", "EmptyName", senior+"/"+junior)
	}
	if _, ok := n.roles[senior]; !ok {
		return nil, nErr(CatNotExist, "DeleteInherit", "RoleNotExist", senior)
	}
	if _, ok := n.roles[junior]; !ok {
		return nil, nErr(CatNotExist, "DeleteInherit", "RoleNotExist", junior)
	}
	if !n.edges[senior][junior] {
		return nil, nErr(CatConflict, "DeleteInherit", "EdgeNotExist", senior+"/"+junior)
	}
	mayLeave := n.juniors(junior)
	affected := map[string]bool{}
	for user := range n.users {
		if n.auth(user)[senior] {
			affected[user] = true
		}
	}
	n.lastUsers = len(affected)
	delete(n.edges[senior], junior)

	candidates := map[string]bool{}
	for sid, s := range n.sessions {
		if !affected[s.owner] {
			continue
		}
		for role := range s.active {
			if mayLeave[role] {
				candidates[sid] = true
				break
			}
		}
	}
	n.lastSessions = len(candidates)

	var pairs []Pair
	for _, sid := range sortedOf(candidates) {
		s := n.sessions[sid]
		newAuth := n.auth(s.owner)
		var dropped []string
		for role := range s.active {
			if !newAuth[role] {
				dropped = append(dropped, role)
			}
		}
		sort.Strings(dropped)
		for _, role := range dropped {
			delete(s.active, role)
			pairs = append(pairs, Pair{Session: sid, Role: role})
		}
	}
	return pairs, nil
}

func (n *naive) AssignUser(user, role string) error {
	if user == "" || role == "" {
		return nErr(CatInvalidParam, "AssignUser", "EmptyName", user+"/"+role)
	}
	if _, ok := n.users[user]; !ok {
		return nErr(CatNotExist, "AssignUser", "UserNotExist", user)
	}
	if _, ok := n.roles[role]; !ok {
		return nErr(CatNotExist, "AssignUser", "RoleNotExist", role)
	}
	if n.users[user][role] {
		return nErr(CatConflict, "AssignUser", "DuplicateAssignment", user+"/"+role)
	}
	newAuth := n.auth(user)
	unionInto(newAuth, n.juniors(role))
	minC := ""
	for cname, c := range n.ssd {
		if intersectCount(newAuth, c.rs) >= c.n && (minC == "" || cname < minC) {
			minC = cname
		}
	}
	if minC != "" {
		return &Error{Cat: CatViolation, Op: "AssignUser", Kind: "SSDViolation",
			Constraint: minC, Subject: user}
	}
	n.users[user][role] = true
	return nil
}

func (n *naive) DeassignUser(user, role string) ([]Pair, error) {
	if user == "" || role == "" {
		return nil, nErr(CatInvalidParam, "DeassignUser", "EmptyName", user+"/"+role)
	}
	if _, ok := n.users[user]; !ok {
		return nil, nErr(CatNotExist, "DeassignUser", "UserNotExist", user)
	}
	if _, ok := n.roles[role]; !ok {
		return nil, nErr(CatNotExist, "DeassignUser", "RoleNotExist", role)
	}
	if !n.users[user][role] {
		return nil, nErr(CatConflict, "DeassignUser", "NotAssigned", user+"/"+role)
	}
	delete(n.users[user], role)
	newAuth := n.auth(user)
	var sids []string
	for sid, s := range n.sessions {
		if s.owner == user {
			sids = append(sids, sid)
		}
	}
	sort.Strings(sids)
	var pairs []Pair
	for _, sid := range sids {
		s := n.sessions[sid]
		var dropped []string
		for r := range s.active {
			if !newAuth[r] {
				dropped = append(dropped, r)
			}
		}
		sort.Strings(dropped)
		for _, r := range dropped {
			delete(s.active, r)
			pairs = append(pairs, Pair{Session: sid, Role: r})
		}
	}
	return pairs, nil
}

func (n *naive) validateConstraint(op, name string, rs []string, nn int) (map[string]bool, error) {
	if name == "" {
		return nil, nErr(CatInvalidParam, op, "EmptyName", name)
	}
	if len(rs) < 2 {
		return nil, nErr(CatInvalidParam, op, "BadRS", name)
	}
	set := map[string]bool{}
	for _, role := range rs {
		if role == "" {
			return nil, nErr(CatInvalidParam, op, "EmptyName", name)
		}
		if set[role] {
			return nil, nErr(CatInvalidParam, op, "DuplicateRoleInRS", role)
		}
		set[role] = true
	}
	if nn < 2 || nn > len(rs) {
		return nil, nErr(CatInvalidParam, op, "BadN", name)
	}
	for _, role := range rs {
		if _, ok := n.roles[role]; !ok {
			return nil, nErr(CatNotExist, op, "RoleNotExist", role)
		}
	}
	return set, nil
}

func (n *naive) AddSSD(name string, rs []string, nn int) error {
	set, err := n.validateConstraint("AddSSD", name, rs, nn)
	if err != nil {
		return err
	}
	if _, ok := n.ssd[name]; ok {
		return nErr(CatConflict, "AddSSD", "DuplicateConstraint", name)
	}
	minUser := ""
	for user := range n.users {
		if intersectCount(n.auth(user), set) >= nn && (minUser == "" || user < minUser) {
			minUser = user
		}
	}
	if minUser != "" {
		return &Error{Cat: CatViolation, Op: "AddSSD", Kind: "SSDViolation",
			Constraint: name, Subject: minUser}
	}
	if len(n.ssd)+len(n.dsd) >= n.cmax {
		return nErr(CatLimit, "AddSSD", "ConstraintLimit", name)
	}
	n.ssd[name] = nConstraint{rs: set, n: nn}
	return nil
}

func (n *naive) AddDSD(name string, rs []string, nn int) error {
	set, err := n.validateConstraint("AddDSD", name, rs, nn)
	if err != nil {
		return err
	}
	if _, ok := n.dsd[name]; ok {
		return nErr(CatConflict, "AddDSD", "DuplicateConstraint", name)
	}
	minSid := ""
	for sid := range n.sessions {
		if intersectCount(n.eff(sid), set) >= nn && (minSid == "" || sid < minSid) {
			minSid = sid
		}
	}
	if minSid != "" {
		return &Error{Cat: CatViolation, Op: "AddDSD", Kind: "DSDViolation",
			Constraint: name, Subject: minSid}
	}
	if len(n.ssd)+len(n.dsd) >= n.cmax {
		return nErr(CatLimit, "AddDSD", "ConstraintLimit", name)
	}
	n.dsd[name] = nConstraint{rs: set, n: nn}
	return nil
}

func (n *naive) CreateSession(sid, user string) error {
	if sid == "" || user == "" {
		return nErr(CatInvalidParam, "CreateSession", "EmptyName", sid+"/"+user)
	}
	if _, ok := n.users[user]; !ok {
		return nErr(CatNotExist, "CreateSession", "UserNotExist", user)
	}
	if _, ok := n.sessions[sid]; ok {
		return nErr(CatConflict, "CreateSession", "DuplicateSession", sid)
	}
	count := 0
	for _, s := range n.sessions {
		if s.owner == user {
			count++
		}
	}
	if count >= n.smax {
		return nErr(CatLimit, "CreateSession", "SessionLimit", user)
	}
	n.sessions[sid] = &naiveSession{owner: user, active: map[string]bool{}}
	return nil
}

func (n *naive) DeleteSession(sid string) error {
	if sid == "" {
		return nErr(CatInvalidParam, "DeleteSession", "EmptyName", sid)
	}
	if _, ok := n.sessions[sid]; !ok {
		return nErr(CatNotExist, "DeleteSession", "SessionNotExist", sid)
	}
	delete(n.sessions, sid)
	return nil
}

func (n *naive) Activate(sid, role string) error {
	if sid == "" || role == "" {
		return nErr(CatInvalidParam, "Activate", "EmptyName", sid+"/"+role)
	}
	s, ok := n.sessions[sid]
	if !ok {
		return nErr(CatNotExist, "Activate", "SessionNotExist", sid)
	}
	if _, ok := n.roles[role]; !ok {
		return nErr(CatNotExist, "Activate", "RoleNotExist", role)
	}
	if s.active[role] {
		return nErr(CatConflict, "Activate", "DuplicateActivation", sid+"/"+role)
	}
	if !n.auth(s.owner)[role] {
		return nErr(CatConflict, "Activate", "UnauthorizedActivation", sid+"/"+role)
	}
	newEff := n.eff(sid)
	unionInto(newEff, n.juniors(role))
	minC := ""
	for cname, c := range n.dsd {
		if intersectCount(newEff, c.rs) >= c.n && (minC == "" || cname < minC) {
			minC = cname
		}
	}
	if minC != "" {
		return &Error{Cat: CatViolation, Op: "Activate", Kind: "DSDViolation",
			Constraint: minC, Subject: sid}
	}
	s.active[role] = true
	return nil
}

func (n *naive) Deactivate(sid, role string) error {
	if sid == "" || role == "" {
		return nErr(CatInvalidParam, "Deactivate", "EmptyName", sid+"/"+role)
	}
	s, ok := n.sessions[sid]
	if !ok {
		return nErr(CatNotExist, "Deactivate", "SessionNotExist", sid)
	}
	if _, ok := n.roles[role]; !ok {
		return nErr(CatNotExist, "Deactivate", "RoleNotExist", role)
	}
	if s.active[role] {
		delete(s.active, role)
		return nil
	}
	if n.eff(sid)[role] {
		var impliers []string
		for a := range s.active {
			if n.juniors(a)[role] {
				impliers = append(impliers, a)
			}
		}
		sort.Strings(impliers)
		return &Error{Cat: CatConflict, Op: "Deactivate", Kind: "ImplicitActivation",
			Object: role, Impliers: impliers}
	}
	return nErr(CatConflict, "Deactivate", "NotActivated", role)
}

func (n *naive) Check(sid, perm string) bool {
	s, ok := n.sessions[sid]
	if !ok {
		return false
	}
	for role := range n.eff(sid) {
		if n.roles[role][perm] {
			return true
		}
	}
	_ = s
	return false
}
