package rbac

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, err error, cat Category, kind string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s/%s, got nil", cat, kind)
	}
	var oe *Error
	if !errors.As(err, &oe) {
		t.Fatalf("error has type %T, want *Error", err)
	}
	if oe.Cat != cat || oe.Kind != kind {
		t.Fatalf("got %s/%s, want %s/%s (err=%v)", oe.Cat, oe.Kind, cat, kind, err)
	}
	return oe
}

// --- white-box state inspection helpers ---

func sortedOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func authOf(r *RBAC, user string) []string { return sortedOf(r.users[user].auth) }

func effOf(r *RBAC, sid string) []string { return sortedOf(r.sessions[sid].eff) }

func activeOf(r *RBAC, sid string) []string { return sortedOf(r.sessions[sid].active) }

func pairsToStrings(ps []Pair) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Session+"/"+p.Role)
	}
	return out
}

// digest renders the full logical state deterministically; used to assert
// that rejected operations changed nothing.
func digest(r *RBAC) string {
	var b strings.Builder
	var roles []string
	for role := range r.roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		fmt.Fprintf(&b, "role %s perms=%v\n", role, sortedOf(r.roles[role]))
	}
	var seniors []string
	for s := range r.edges {
		seniors = append(seniors, s)
	}
	sort.Strings(seniors)
	for _, s := range seniors {
		fmt.Fprintf(&b, "edge %s -> %v\n", s, sortedOf(r.edges[s]))
	}
	var users []string
	for u := range r.users {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, u := range users {
		rec := r.users[u]
		fmt.Fprintf(&b, "user %s assigned=%v auth=%v sessions=%v\n",
			u, sortedOf(rec.assigned), sortedOf(rec.auth), sortedOf(rec.sessions))
	}
	var sids []string
	for s := range r.sessions {
		sids = append(sids, s)
	}
	sort.Strings(sids)
	for _, s := range sids {
		rec := r.sessions[s]
		fmt.Fprintf(&b, "session %s owner=%s active=%v eff=%v\n",
			s, rec.owner, sortedOf(rec.active), sortedOf(rec.eff))
	}
	var cs []string
	for c := range r.ssd {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	for _, c := range cs {
		fmt.Fprintf(&b, "ssd %s rs=%v n=%d\n", c, sortedOf(r.ssd[c].rs), r.ssd[c].n)
	}
	cs = cs[:0]
	for c := range r.dsd {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	for _, c := range cs {
		fmt.Fprintf(&b, "dsd %s rs=%v n=%d\n", c, sortedOf(r.dsd[c].rs), r.dsd[c].n)
	}
	return b.String()
}

func newRBAC(t *testing.T, smax, cmax int) *RBAC {
	t.Helper()
	r, err := NewRBAC(smax, cmax)
	if err != nil {
		t.Fatalf("NewRBAC(%d, %d): %v", smax, cmax, err)
	}
	return r
}

func addRoles(t *testing.T, r *RBAC, roles ...string) {
	t.Helper()
	for _, role := range roles {
		mustOK(t, r.AddRole(role))
	}
}

func TestNewRBACConfigValidation(t *testing.T) {
	for _, cfg := range [][2]int{{0, 1}, {1, 0}, {-1, 5}, {5, -1}, {1001, 1}, {1, 1001}, {0, 0}} {
		if _, err := NewRBAC(cfg[0], cfg[1]); err == nil {
			t.Fatalf("NewRBAC%v: expected rejection", cfg)
		} else {
			mustErr(t, err, CatInvalidParam, "InvalidConfig")
		}
	}
	for _, cfg := range [][2]int{{1, 1}, {1000, 1000}, {3, 7}} {
		if _, err := NewRBAC(cfg[0], cfg[1]); err != nil {
			t.Fatalf("NewRBAC%v: unexpected error %v", cfg, err)
		}
	}
}

// Spec example: lead is senior of dev; SSD s1 over {dev, ops, audit} with
// n=2 counts inherited roles, so alice={lead, ops} violates via dev.
func TestSpecExampleSSDCountsInheritedRoles(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "lead", "dev", "ops", "audit")
	mustOK(t, r.AddInherit("lead", "dev"))
	mustOK(t, r.AddSSD("s1", []string{"dev", "ops", "audit"}, 2))
	mustOK(t, r.AddUser("alice"))
	mustOK(t, r.AssignUser("alice", "lead"))
	if got, want := authOf(r, "alice"), []string{"dev", "lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Auth(alice)=%v, want %v", got, want)
	}
	before := digest(r)
	e := mustErr(t, r.AssignUser("alice", "ops"), CatViolation, "SSDViolation")
	if e.Constraint != "s1" || e.Subject != "alice" {
		t.Fatalf("violation=(%s,%s), want (s1,alice)", e.Constraint, e.Subject)
	}
	if after := digest(r); after != before {
		t.Fatalf("rejected AssignUser changed state:\n%s", after)
	}
}

// Spec example: AddInherit(boss, r2) is rejected because bob's new Auth
// would violate s2, and the edge is not added.
func TestSpecExampleAddInheritRejectedNoSideEffect(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "boss", "r1", "r2")
	mustOK(t, r.AddSSD("s2", []string{"r1", "r2"}, 2))
	mustOK(t, r.AddUser("bob"))
	mustOK(t, r.AssignUser("bob", "boss"))
	mustOK(t, r.AddInherit("boss", "r1"))
	if got, want := authOf(r, "bob"), []string{"boss", "r1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Auth(bob)=%v, want %v", got, want)
	}
	before := digest(r)
	e := mustErr(t, r.AddInherit("boss", "r2"), CatViolation, "SSDViolation")
	if e.Constraint != "s2" || e.Subject != "bob" {
		t.Fatalf("violation=(%s,%s), want (s2,bob)", e.Constraint, e.Subject)
	}
	if after := digest(r); after != before {
		t.Fatalf("rejected AddInherit changed state:\n%s", after)
	}
	// The edge really was not added.
	_, derr := r.DeleteInherit("boss", "r2")
	mustErr(t, derr, CatConflict, "EdgeNotExist")
	if got, want := authOf(r, "bob"), []string{"boss", "r1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Auth(bob)=%v, want %v", got, want)
	}
}

// Spec example: cascading deactivation. DeassignUser keeps dev because it is
// still reachable via lead; DeleteInherit(lead, dev) then deactivates the
// explicit dev and recomputes Eff.
func TestSpecExampleCascade(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "lead", "dev")
	mustOK(t, r.AddInherit("lead", "dev"))
	mustOK(t, r.AssignPerm("lead", "plead"))
	mustOK(t, r.AssignPerm("dev", "pdev"))
	mustOK(t, r.AddUser("dave"))
	mustOK(t, r.AssignUser("dave", "lead"))
	mustOK(t, r.AssignUser("dave", "dev"))
	mustOK(t, r.CreateSession("s1", "dave"))
	mustOK(t, r.Activate("s1", "lead"))
	mustOK(t, r.Activate("s1", "dev"))

	pairs, err := r.DeassignUser("dave", "dev")
	mustOK(t, err)
	if len(pairs) != 0 {
		t.Fatalf("DeassignUser deactivated %v, want empty (dev still reachable via lead)", pairs)
	}
	if got, want := authOf(r, "dave"), []string{"dev", "lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Auth(dave)=%v, want %v", got, want)
	}

	pairs, err = r.DeleteInherit("lead", "dev")
	mustOK(t, err)
	if got, want := pairsToStrings(pairs), []string{"s1/dev"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DeleteInherit deactivated %v, want %v", got, want)
	}
	if got, want := authOf(r, "dave"), []string{"lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Auth(dave)=%v, want %v", got, want)
	}
	if got, want := effOf(r, "s1"), []string{"lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Eff(s1)=%v, want %v", got, want)
	}
	if !r.Check("s1", "plead") || r.Check("s1", "pdev") {
		t.Fatalf("Check after cascade: plead=%v pdev=%v, want true/false",
			r.Check("s1", "plead"), r.Check("s1", "pdev"))
	}
}

// Spec example: DSD with implicit activation. Activating ops violates d1
// because lead already brings dev into Eff; dev may still be explicitly
// activated (Eff unchanged); deactivating the implicit dev reports impliers.
func TestSpecExampleDSDImplicit(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "lead", "dev", "ops")
	mustOK(t, r.AddInherit("lead", "dev"))
	mustOK(t, r.AddDSD("d1", []string{"dev", "ops"}, 2))
	mustOK(t, r.AddUser("carol"))
	mustOK(t, r.AssignUser("carol", "lead"))
	mustOK(t, r.AssignUser("carol", "ops"))
	mustOK(t, r.CreateSession("sc", "carol"))
	mustOK(t, r.Activate("sc", "lead"))
	if got, want := effOf(r, "sc"), []string{"dev", "lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Eff(sc)=%v, want %v", got, want)
	}

	// Eff would become {lead, dev, ops}: |{dev, ops} ∩ RS| = 2 >= n.
	before := digest(r)
	e := mustErr(t, r.Activate("sc", "ops"), CatViolation, "DSDViolation")
	if e.Constraint != "d1" || e.Subject != "sc" {
		t.Fatalf("violation=(%s,%s), want (d1,sc)", e.Constraint, e.Subject)
	}
	if after := digest(r); after != before {
		t.Fatalf("rejected Activate changed state:\n%s", after)
	}

	// dev is only implicitly active: deactivation reports the impliers.
	e = mustErr(t, r.Deactivate("sc", "dev"), CatConflict, "ImplicitActivation")
	if !reflect.DeepEqual(e.Impliers, []string{"lead"}) {
		t.Fatalf("impliers=%v, want [lead]", e.Impliers)
	}

	// Explicitly activating the implied dev is allowed and changes nothing
	// about Eff.
	mustOK(t, r.Activate("sc", "dev"))
	if got, want := effOf(r, "sc"), []string{"dev", "lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Eff(sc)=%v, want %v", got, want)
	}
	if got, want := activeOf(r, "sc"), []string{"dev", "lead"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("A(sc)=%v, want %v", got, want)
	}

	mustOK(t, r.Deactivate("sc", "lead"))
	if got, want := activeOf(r, "sc"), []string{"dev"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("A(sc)=%v, want %v", got, want)
	}
	if got, want := effOf(r, "sc"), []string{"dev"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Eff(sc)=%v, want %v", got, want)
	}
}

// Boundary at n: intersection of size n-1 passes, size n violates.
func TestBoundaryNMinusOnePassesNErrors(t *testing.T) {
	// SSD with |RS|=3, n=3.
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "a", "b", "c")
	mustOK(t, r.AddSSD("s", []string{"a", "b", "c"}, 3))
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "a"))
	mustOK(t, r.AssignUser("u", "b"))                                // count 2 = n-1: passes
	mustErr(t, r.AssignUser("u", "c"), CatViolation, "SSDViolation") // count 3 = n

	// DSD with |RS|=2, n=2.
	r2 := newRBAC(t, 10, 10)
	addRoles(t, r2, "x", "y")
	mustOK(t, r2.AddDSD("d", []string{"x", "y"}, 2))
	mustOK(t, r2.AddUser("u"))
	mustOK(t, r2.AssignUser("u", "x"))
	mustOK(t, r2.AssignUser("u", "y"))
	mustOK(t, r2.CreateSession("s", "u"))
	mustOK(t, r2.Activate("s", "x"))                                // count 1 = n-1: passes
	mustErr(t, r2.Activate("s", "y"), CatViolation, "DSDViolation") // count 2 = n
}

// A new constraint that existing data already violates is rejected and not
// registered; the reported subject is the byte-order-minimum violator.
func TestAddConstraintRejectedByExistingData(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "a", "b")
	for _, u := range []string{"ub", "ua"} {
		mustOK(t, r.AddUser(u))
		mustOK(t, r.AssignUser(u, "a"))
		mustOK(t, r.AssignUser(u, "b"))
	}
	before := digest(r)
	e := mustErr(t, r.AddSSD("s", []string{"a", "b"}, 2), CatViolation, "SSDViolation")
	if e.Constraint != "s" || e.Subject != "ua" {
		t.Fatalf("violation=(%s,%s), want (s,ua)", e.Constraint, e.Subject)
	}
	if after := digest(r); after != before {
		t.Fatalf("rejected AddSSD changed state:\n%s", after)
	}
	// Not registered: retrying reports the violation again, not a duplicate.
	mustErr(t, r.AddSSD("s", []string{"a", "b"}, 2), CatViolation, "SSDViolation")

	// Same for DSD against existing sessions.
	mustOK(t, r.CreateSession("s2", "ua"))
	mustOK(t, r.CreateSession("s1", "ub"))
	mustOK(t, r.Activate("s2", "a"))
	mustOK(t, r.Activate("s2", "b"))
	mustOK(t, r.Activate("s1", "a"))
	mustOK(t, r.Activate("s1", "b"))
	e = mustErr(t, r.AddDSD("d", []string{"a", "b"}, 2), CatViolation, "DSDViolation")
	if e.Constraint != "d" || e.Subject != "s1" {
		t.Fatalf("violation=(%s,%s), want (d,s1)", e.Constraint, e.Subject)
	}
}

// SSD and DSD constraint names live in independent namespaces.
func TestSSDDSDIndependentNamespaces(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "a", "b")
	mustOK(t, r.AddSSD("same", []string{"a", "b"}, 2))
	mustOK(t, r.AddDSD("same", []string{"a", "b"}, 2))
	mustErr(t, r.AddSSD("same", []string{"a", "b"}, 2), CatConflict, "DuplicateConstraint")
	mustErr(t, r.AddDSD("same", []string{"a", "b"}, 2), CatConflict, "DuplicateConstraint")
}

// Unauthorized activation, duplicate activation, implicit deactivation and
// not-activated deactivation are distinct conflicts.
func TestActivationConflictKinds(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "senior", "junior", "other")
	mustOK(t, r.AddInherit("senior", "junior"))
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "senior"))
	mustOK(t, r.CreateSession("s", "u"))

	// other is not in Auth(u).
	mustErr(t, r.Activate("s", "other"), CatConflict, "UnauthorizedActivation")
	mustOK(t, r.Activate("s", "senior"))
	// senior is already explicitly activated.
	mustErr(t, r.Activate("s", "senior"), CatConflict, "DuplicateActivation")
	// junior is only implied by senior.
	e := mustErr(t, r.Deactivate("s", "junior"), CatConflict, "ImplicitActivation")
	if !reflect.DeepEqual(e.Impliers, []string{"senior"}) {
		t.Fatalf("impliers=%v, want [senior]", e.Impliers)
	}
	// other is not active at all.
	mustErr(t, r.Deactivate("s", "other"), CatConflict, "NotActivated")
}

// Multiple impliers are reported sorted ascending.
func TestImplicitActivationMultipleImpliersSorted(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "zeta", "mid", "alpha", "leaf")
	mustOK(t, r.AddInherit("zeta", "leaf"))
	mustOK(t, r.AddInherit("alpha", "leaf"))
	mustOK(t, r.AddInherit("mid", "leaf"))
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "zeta"))
	mustOK(t, r.AssignUser("u", "alpha"))
	mustOK(t, r.AssignUser("u", "mid"))
	mustOK(t, r.CreateSession("s", "u"))
	mustOK(t, r.Activate("s", "zeta"))
	mustOK(t, r.Activate("s", "alpha"))
	mustOK(t, r.Activate("s", "mid"))
	e := mustErr(t, r.Deactivate("s", "leaf"), CatConflict, "ImplicitActivation")
	if !reflect.DeepEqual(e.Impliers, []string{"alpha", "mid", "zeta"}) {
		t.Fatalf("impliers=%v, want [alpha mid zeta]", e.Impliers)
	}
}

// A violation report carries the byte-order-minimum constraint name, and
// under it the byte-order-minimum user/session name.
func TestViolationReportsMinimumNames(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "a", "b")
	mustOK(t, r.AddSSD("sz", []string{"a", "b"}, 2))
	mustOK(t, r.AddSSD("sa", []string{"a", "b"}, 2))
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "a"))
	// Both "sa" and "sz" are violated; the minimum name wins.
	e := mustErr(t, r.AssignUser("u", "b"), CatViolation, "SSDViolation")
	if e.Constraint != "sa" || e.Subject != "u" {
		t.Fatalf("violation=(%s,%s), want (sa,u)", e.Constraint, e.Subject)
	}

	// Minimum user name under the minimum constraint.
	r2 := newRBAC(t, 10, 10)
	addRoles(t, r2, "boss", "x", "y")
	mustOK(t, r2.AddUser("ub"))
	mustOK(t, r2.AddUser("ua"))
	mustOK(t, r2.AssignUser("ub", "boss"))
	mustOK(t, r2.AssignUser("ua", "boss"))
	mustOK(t, r2.AddInherit("boss", "x"))
	mustOK(t, r2.AddSSD("s1", []string{"x", "y"}, 2))
	e = mustErr(t, r2.AddInherit("boss", "y"), CatViolation, "SSDViolation")
	if e.Constraint != "s1" || e.Subject != "ua" {
		t.Fatalf("violation=(%s,%s), want (s1,ua)", e.Constraint, e.Subject)
	}
	// Both affected users were checked; the DSD phase was never entered.
	users, sessions := r2.LastCheckCounts()
	if users != 2 || sessions != 0 {
		t.Fatalf("counts=(%d,%d), want (2,0)", users, sessions)
	}
}

// When AddInherit would violate both an SSD and a DSD constraint, the SSD
// violation is reported and the DSD phase is not entered (session count 0).
func TestSSDViolationPrecedesDSD(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "boss", "x", "y")
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "boss"))
	mustOK(t, r.AddInherit("boss", "x"))
	mustOK(t, r.CreateSession("s", "u"))
	mustOK(t, r.Activate("s", "boss")) // Eff(s) = {boss, x}
	mustOK(t, r.AddSSD("sd", []string{"x", "y"}, 2))
	mustOK(t, r.AddDSD("dd", []string{"x", "y"}, 2))
	before := digest(r)
	e := mustErr(t, r.AddInherit("boss", "y"), CatViolation, "SSDViolation")
	if e.Constraint != "sd" || e.Subject != "u" {
		t.Fatalf("violation=(%s,%s), want (sd,u)", e.Constraint, e.Subject)
	}
	users, sessions := r.LastCheckCounts()
	if users != 1 || sessions != 0 {
		t.Fatalf("counts=(%d,%d), want (1,0)", users, sessions)
	}
	if after := digest(r); after != before {
		t.Fatalf("rejected AddInherit changed state:\n%s", after)
	}
}

// DSD violation reported by AddInherit when no SSD is violated; all affected
// sessions are checked and the minimum names are reported.
func TestAddInheritDSDViolation(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "boss", "x", "y")
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "boss"))
	mustOK(t, r.AddInherit("boss", "x"))
	mustOK(t, r.CreateSession("sb", "u"))
	mustOK(t, r.CreateSession("sa", "u"))
	mustOK(t, r.Activate("sb", "boss"))
	mustOK(t, r.Activate("sa", "boss"))
	mustOK(t, r.AddDSD("d1", []string{"x", "y"}, 2))
	before := digest(r)
	e := mustErr(t, r.AddInherit("boss", "y"), CatViolation, "DSDViolation")
	if e.Constraint != "d1" || e.Subject != "sa" {
		t.Fatalf("violation=(%s,%s), want (d1,sa)", e.Constraint, e.Subject)
	}
	users, sessions := r.LastCheckCounts()
	if users != 1 || sessions != 2 {
		t.Fatalf("counts=(%d,%d), want (1,2)", users, sessions)
	}
	if after := digest(r); after != before {
		t.Fatalf("rejected AddInherit changed state:\n%s", after)
	}
}

func TestSessionLimit(t *testing.T) {
	r := newRBAC(t, 2, 10)
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.CreateSession("s1", "u"))
	mustOK(t, r.CreateSession("s2", "u"))
	before := digest(r)
	mustErr(t, r.CreateSession("s3", "u"), CatLimit, "SessionLimit")
	if after := digest(r); after != before {
		t.Fatalf("rejected CreateSession changed state:\n%s", after)
	}
	// Another user is unaffected by u's limit.
	mustOK(t, r.AddUser("v"))
	mustOK(t, r.CreateSession("t1", "v"))
}

func TestConstraintLimit(t *testing.T) {
	r := newRBAC(t, 10, 2)
	addRoles(t, r, "a", "b", "c", "d")
	mustOK(t, r.AddSSD("c1", []string{"a", "b"}, 2))
	mustOK(t, r.AddDSD("c2", []string{"a", "b"}, 2))
	mustErr(t, r.AddSSD("c3", []string{"c", "d"}, 2), CatLimit, "ConstraintLimit")
	mustErr(t, r.AddDSD("c4", []string{"c", "d"}, 2), CatLimit, "ConstraintLimit")

	// Violation is reported before the limit (category order).
	r2 := newRBAC(t, 10, 1)
	addRoles(t, r2, "a", "b", "c", "d")
	mustOK(t, r2.AddSSD("c1", []string{"a", "b"}, 2))
	mustOK(t, r2.AddUser("u"))
	mustOK(t, r2.AssignUser("u", "c"))
	mustOK(t, r2.AssignUser("u", "d"))
	mustErr(t, r2.AddSSD("c2", []string{"c", "d"}, 2), CatViolation, "SSDViolation")
}

func TestInvalidParamsAndNotExist(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "a", "b")
	mustOK(t, r.AddUser("u"))

	mustErr(t, r.AddRole(""), CatInvalidParam, "EmptyName")
	mustErr(t, r.AddUser(""), CatInvalidParam, "EmptyName")
	mustErr(t, r.AddInherit("", "a"), CatInvalidParam, "EmptyName")
	mustErr(t, r.AssignUser("u", ""), CatInvalidParam, "EmptyName")
	mustErr(t, r.CreateSession("", "u"), CatInvalidParam, "EmptyName")
	mustErr(t, r.AssignPerm("a", ""), CatInvalidParam, "EmptyName")

	// Not-exist follows parameter order: user before role.
	mustErr(t, r.AssignUser("nouser", "norole"), CatNotExist, "UserNotExist")
	mustErr(t, r.AssignUser("u", "norole"), CatNotExist, "RoleNotExist")
	e := mustErr(t, r.AddInherit("no1", "no2"), CatNotExist, "RoleNotExist")
	if e.Object != "no1" {
		t.Fatalf("object=%s, want no1", e.Object)
	}

	// Constraint parameter validation.
	mustErr(t, r.AddSSD("", []string{"a", "b"}, 2), CatInvalidParam, "EmptyName")
	mustErr(t, r.AddSSD("s", []string{"a"}, 1), CatInvalidParam, "BadRS")
	mustErr(t, r.AddSSD("s", []string{"a", "a"}, 2), CatInvalidParam, "DuplicateRoleInRS")
	mustErr(t, r.AddSSD("s", []string{"a", "b"}, 1), CatInvalidParam, "BadN")
	mustErr(t, r.AddSSD("s", []string{"a", "b"}, 3), CatInvalidParam, "BadN")
	mustErr(t, r.AddSSD("s", []string{"a", ""}, 2), CatInvalidParam, "EmptyName")
	// First missing role in RS order is reported.
	e = mustErr(t, r.AddSSD("s", []string{"a", "zz", "yy"}, 2), CatNotExist, "RoleNotExist")
	if e.Object != "zz" {
		t.Fatalf("object=%s, want zz", e.Object)
	}
	e = mustErr(t, r.AddDSD("s", []string{"yy", "a"}, 2), CatNotExist, "RoleNotExist")
	if e.Object != "yy" {
		t.Fatalf("object=%s, want yy", e.Object)
	}
}

func TestConflictKinds(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "a", "b", "c")
	mustOK(t, r.AddUser("u"))
	mustErr(t, r.AddRole("a"), CatConflict, "DuplicateRole")
	mustErr(t, r.AddUser("u"), CatConflict, "DuplicateUser")
	mustOK(t, r.AddInherit("a", "b"))
	mustErr(t, r.AddInherit("a", "b"), CatConflict, "DuplicateEdge")
	mustErr(t, r.AddInherit("a", "a"), CatConflict, "Cycle")
	mustErr(t, r.AddInherit("b", "a"), CatConflict, "Cycle")
	mustOK(t, r.AddInherit("b", "c"))
	mustErr(t, r.AddInherit("c", "a"), CatConflict, "Cycle") // a reaches c via b
	mustOK(t, r.AssignUser("u", "a"))
	mustErr(t, r.AssignUser("u", "a"), CatConflict, "DuplicateAssignment")
	_, daerr := r.DeassignUser("u", "b")
	mustErr(t, daerr, CatConflict, "NotAssigned")
	mustOK(t, r.CreateSession("s", "u"))
	mustErr(t, r.CreateSession("s", "u"), CatConflict, "DuplicateSession")
	mustOK(t, r.AssignPerm("a", "p"))
	mustErr(t, r.AssignPerm("a", "p"), CatConflict, "DuplicatePerm")
	_, derr := r.DeleteInherit("a", "c")
	mustErr(t, derr, CatConflict, "EdgeNotExist")
}

// The cascade list contains only explicitly activated roles, sorted by
// session id then role name; Eff is recomputed afterwards.
func TestCascadeListOrderAndExplicitOnly(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "p", "q1", "q2", "q3")
	mustOK(t, r.AddInherit("p", "q1"))
	mustOK(t, r.AddInherit("p", "q2"))
	mustOK(t, r.AddInherit("q1", "q3"))
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "p"))
	mustOK(t, r.CreateSession("s0", "u"))
	mustOK(t, r.CreateSession("s1", "u"))
	mustOK(t, r.CreateSession("s2", "u"))
	mustOK(t, r.Activate("s0", "q3"))
	mustOK(t, r.Activate("s1", "q1"))
	mustOK(t, r.Activate("s1", "q3"))
	mustOK(t, r.Activate("s1", "q2"))
	mustOK(t, r.Activate("s2", "p")) // q1/q3 only implicit here

	pairs, err := r.DeleteInherit("p", "q1")
	mustOK(t, err)
	want := []string{"s0/q3", "s1/q1", "s1/q3"}
	if got := pairsToStrings(pairs); !reflect.DeepEqual(got, want) {
		t.Fatalf("deactivated=%v, want %v", got, want)
	}
	if got := effOf(r, "s0"); len(got) != 0 {
		t.Fatalf("Eff(s0)=%v, want empty", got)
	}
	if got, w := effOf(r, "s1"), []string{"q2"}; !reflect.DeepEqual(got, w) {
		t.Fatalf("Eff(s1)=%v, want %v", got, w)
	}
	// s2 kept its explicit p; its Eff shrank to what p still reaches.
	if got, w := effOf(r, "s2"), []string{"p", "q2"}; !reflect.DeepEqual(got, w) {
		t.Fatalf("Eff(s2)=%v, want %v", got, w)
	}
}

// DeassignUser cascades through every session of the user, also sorted.
func TestDeassignUserCascadeOrder(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "a", "b")
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "a"))
	mustOK(t, r.AssignUser("u", "b"))
	mustOK(t, r.CreateSession("s2", "u"))
	mustOK(t, r.CreateSession("s1", "u"))
	mustOK(t, r.Activate("s2", "b"))
	mustOK(t, r.Activate("s2", "a"))
	mustOK(t, r.Activate("s1", "b"))
	pairs, err := r.DeassignUser("u", "b")
	mustOK(t, err)
	want := []string{"s1/b", "s2/b"}
	if got := pairsToStrings(pairs); !reflect.DeepEqual(got, want) {
		t.Fatalf("deactivated=%v, want %v", got, want)
	}
	if got, w := effOf(r, "s2"), []string{"a"}; !reflect.DeepEqual(got, w) {
		t.Fatalf("Eff(s2)=%v, want %v", got, w)
	}
}

func TestCheckPermissions(t *testing.T) {
	r := newRBAC(t, 10, 10)
	addRoles(t, r, "senior", "junior")
	mustOK(t, r.AddInherit("senior", "junior"))
	mustOK(t, r.AssignPerm("senior", "ps"))
	mustOK(t, r.AssignPerm("junior", "pj"))
	mustOK(t, r.AddUser("u"))
	mustOK(t, r.AssignUser("u", "senior"))
	mustOK(t, r.CreateSession("s", "u"))
	if r.Check("s", "ps") || r.Check("s", "pj") {
		t.Fatal("nothing active yet: Check must be false")
	}
	if r.Check("nosuch", "ps") {
		t.Fatal("unknown session: Check must be false")
	}
	mustOK(t, r.Activate("s", "senior"))
	// Activating senior also grants junior's permissions via inheritance.
	if !r.Check("s", "ps") || !r.Check("s", "pj") {
		t.Fatal("senior active: both perms must be visible")
	}
	if r.Check("s", "nope") {
		t.Fatal("unknown perm: Check must be false")
	}
	mustOK(t, r.Deactivate("s", "senior"))
	if r.Check("s", "ps") || r.Check("s", "pj") {
		t.Fatal("deactivated: Check must be false")
	}
}
