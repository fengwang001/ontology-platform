package rbac

import (
	"fmt"
	"sort"
	"sync"
)

// Category classifies rejections. Categories are checked in the order
// InvalidParam -> NotExist -> Conflict -> Violation -> Limit and only the
// first matching one is reported.
type Category int

const (
	CatInvalidParam Category = iota // bad constructor args, empty names, bad RS/n
	CatNotExist                     // missing role/user/session
	CatConflict                     // duplicates, cycles, unauthorized, etc.
	CatViolation                    // SSD/DSD constraint violation (SSD first)
	CatLimit                        // session/constraint count limit exceeded
)

func (c Category) String() string {
	switch c {
	case CatInvalidParam:
		return "invalid-param"
	case CatNotExist:
		return "not-exist"
	case CatConflict:
		return "conflict"
	case CatViolation:
		return "violation"
	case CatLimit:
		return "limit"
	}
	return "unknown"
}

// Error describes a rejected operation with locating information.
type Error struct {
	Cat    Category // broad category
	Op     string   // operation name, e.g. "AddInherit"
	Kind   string   // concrete reason, e.g. "Cycle", "SSDViolation"
	Object string   // offending object name (role/user/session/constraint...)

	// Constraint violation locating info.
	Constraint string // violated constraint name (byte-order minimum)
	Subject    string // violating user (SSD) or session (DSD), byte-order minimum

	// Impliers lists, for an implicit-activation conflict, the explicitly
	// activated roles that imply the target role, sorted ascending.
	Impliers []string
}

func (e *Error) Error() string {
	switch e.Cat {
	case CatViolation:
		return fmt.Sprintf("%s: %s: constraint %q violated by %q", e.Op, e.Kind, e.Constraint, e.Subject)
	case CatConflict:
		if e.Kind == "ImplicitActivation" {
			return fmt.Sprintf("%s: %s: %q implied by %v", e.Op, e.Kind, e.Object, e.Impliers)
		}
		return fmt.Sprintf("%s: %s: %q", e.Op, e.Kind, e.Object)
	default:
		return fmt.Sprintf("%s: %s: %q", e.Op, e.Kind, e.Object)
	}
}

// Pair is a (session, role) deactivated pair returned by cascading operations.
type Pair struct {
	Session string
	Role    string
}

type constraint struct {
	name string
	rs   map[string]bool
	n    int
}

type userRec struct {
	assigned map[string]bool // directly assigned roles
	auth     map[string]bool // cached Auth(u)
	sessions map[string]bool // session ids owned by the user
}

type sessionRec struct {
	owner  string
	active map[string]bool // A(s): explicitly activated roles
	eff    map[string]bool // cached Eff(s)
}

// RBAC is the hierarchical-role RBAC manager with SSD/DSD constraints.
type RBAC struct {
	mu   sync.RWMutex
	smax int
	cmax int

	roles    map[string]map[string]bool // role -> permission set
	users    map[string]*userRec        // user -> record
	edges    map[string]map[string]bool // senior -> direct juniors
	ssd      map[string]*constraint     // SSD namespace
	dsd      map[string]*constraint     // DSD namespace (independent)
	ncons    int                        // total constraints (SSD + DSD)
	sessions map[string]*sessionRec     // sid -> record

	// Reverse indexes so that constraint checks and cascades only touch
	// affected users/sessions regardless of how many unrelated ones exist.
	authIdx map[string]map[string]bool // role -> users u with role in Auth(u)
	effIdx  map[string]map[string]bool // role -> sessions s with role in Eff(s)
	explIdx map[string]map[string]bool // role -> sessions s with role in A(s)

	// Unexported counters recording, for the most recent AddInherit or
	// DeleteInherit call, how many users were checked/recomputed and how
	// many sessions were checked/evaluated for deactivation.
	lastUsersChecked    int
	lastSessionsChecked int
}

// NewRBAC creates a manager. Smax is the per-user session limit and Cmax the
// total (SSD + DSD) constraint limit; both must be in [1, 1000], otherwise
// the whole configuration is rejected.
func NewRBAC(smax, cmax int) (*RBAC, error) {
	if smax < 1 || smax > 1000 || cmax < 1 || cmax > 1000 {
		return nil, &Error{Cat: CatInvalidParam, Op: "NewRBAC", Kind: "InvalidConfig",
			Object: fmt.Sprintf("smax=%d cmax=%d", smax, cmax)}
	}
	return &RBAC{
		smax:     smax,
		cmax:     cmax,
		roles:    map[string]map[string]bool{},
		users:    map[string]*userRec{},
		edges:    map[string]map[string]bool{},
		ssd:      map[string]*constraint{},
		dsd:      map[string]*constraint{},
		sessions: map[string]*sessionRec{},
		authIdx:  map[string]map[string]bool{},
		effIdx:   map[string]map[string]bool{},
		explIdx:  map[string]map[string]bool{},
	}, nil
}

// LastCheckCounts reports the unexported counters of the most recent
// AddInherit or DeleteInherit call: users checked/recomputed and sessions
// checked/evaluated for deactivation.
func (r *RBAC) LastCheckCounts() (users, sessions int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastUsersChecked, r.lastSessionsChecked
}

// --- set / index helpers ---

func idxAdd(idx map[string]map[string]bool, key, val string) {
	set, ok := idx[key]
	if !ok {
		set = map[string]bool{}
		idx[key] = set
	}
	set[val] = true
}

func idxDel(idx map[string]map[string]bool, key, val string) {
	if set, ok := idx[key]; ok {
		delete(set, val)
		if len(set) == 0 {
			delete(idx, key)
		}
	}
}

func unionInto(dst, src map[string]bool) {
	for k := range src {
		dst[k] = true
	}
}

func intersectCount(a, b map[string]bool) int {
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// juniors returns Juniors(role): the role itself plus all roles reachable
// along inheritance edges.
func (r *RBAC) juniors(role string) map[string]bool {
	seen := map[string]bool{role: true}
	stack := []string{role}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for j := range r.edges[x] {
			if !seen[j] {
				seen[j] = true
				stack = append(stack, j)
			}
		}
	}
	return seen
}

// computeAuth recomputes Auth(u) from scratch (assigned roles' Juniors union).
func (r *RBAC) computeAuth(u *userRec) map[string]bool {
	auth := map[string]bool{}
	for a := range u.assigned {
		unionInto(auth, r.juniors(a))
	}
	return auth
}

// computeEff recomputes Eff(s) from scratch (explicit roles' Juniors union).
func (r *RBAC) computeEff(s *sessionRec) map[string]bool {
	eff := map[string]bool{}
	for a := range s.active {
		unionInto(eff, r.juniors(a))
	}
	return eff
}

// setAuth replaces the cached Auth of a user and maintains the reverse index.
func (r *RBAC) setAuth(name string, u *userRec, auth map[string]bool) {
	for role := range u.auth {
		if !auth[role] {
			idxDel(r.authIdx, role, name)
		}
	}
	for role := range auth {
		if !u.auth[role] {
			idxAdd(r.authIdx, role, name)
		}
	}
	u.auth = auth
}

// setEff replaces the cached Eff of a session and maintains the reverse index.
func (r *RBAC) setEff(sid string, s *sessionRec, eff map[string]bool) {
	for role := range s.eff {
		if !eff[role] {
			idxDel(r.effIdx, role, sid)
		}
	}
	for role := range eff {
		if !s.eff[role] {
			idxAdd(r.effIdx, role, sid)
		}
	}
	s.eff = eff
}

// violation locates one constraint violation.
type violation struct {
	constraint string
	subject    string
}

// pickMin selects the violation with byte-order-minimum constraint name,
// then byte-order-minimum subject name.
func pickMin(vs []violation) violation {
	min := vs[0]
	for _, v := range vs[1:] {
		if v.constraint < min.constraint ||
			(v.constraint == min.constraint && v.subject < min.subject) {
			min = v
		}
	}
	return min
}

// ssdViolationsAgainst returns the names of all SSD constraints violated by
// the given authorization set: |auth ∩ RS| >= n.
func (r *RBAC) ssdViolationsAgainst(auth map[string]bool) []string {
	var out []string
	for name, c := range r.ssd {
		if intersectCount(auth, c.rs) >= c.n {
			out = append(out, name)
		}
	}
	return out
}

// dsdViolationsAgainst returns the names of all DSD constraints violated by
// the given effective activation set: |eff ∩ RS| >= n.
func (r *RBAC) dsdViolationsAgainst(eff map[string]bool) []string {
	var out []string
	for name, c := range r.dsd {
		if intersectCount(eff, c.rs) >= c.n {
			out = append(out, name)
		}
	}
	return out
}
