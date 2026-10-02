package ontology

import (
	"fmt"
	"reflect"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertKind(t *testing.T, err error, kind ErrorKind, constraint, object string) {
	t.Helper()
	e, ok := err.(*RBACError)
	if !ok || e.Kind != kind {
		t.Fatalf("got %v, want kind %s", err, kind)
	}
	if e.ConstraintName != constraint || e.ObjectName != object {
		t.Fatalf("got constraint %q object %q, want %q %q", e.ConstraintName, e.ObjectName, constraint, object)
	}
}

func TestNewRejectsInvalidLimits(t *testing.T) {
	for _, limits := range [][2]int{{0, 1}, {1001, 1}, {1, 0}, {1, 1001}} {
		if _, err := New(limits[0], limits[1]); !IsInvalid(err) {
			t.Fatalf("limits %v: got %v, want invalid", limits, err)
		}
	}
}

func TestSSDBoundaryAndInheritedCount(t *testing.T) {
	m, err := New(2, 10)
	must(t, err)
	for _, role := range []string{"dev", "lead", "ops", "audit"} {
		must(t, m.AddRole(role))
	}
	must(t, m.AddUser("alice"))
	must(t, m.AddInherit("lead", "dev"))
	must(t, m.AddSSD("s1", []string{"dev", "ops", "audit"}, 2))
	must(t, m.AssignUser("alice", "lead"))
	must(t, m.AssignPerm("dev", "read"))

	if len(m.authForTest("alice")) != 2 {
		t.Fatalf("auth = %v", m.authForTest("alice"))
	}
	err = m.AssignUser("alice", "ops")
	assertKind(t, err, ErrViolation, "s1", "alice")
	if containsSet(m.userRoles["alice"], "ops") {
		t.Fatal("rejected assignment was stored")
	}
}

func TestAddInheritDownstreamRejectionNoSideEffect(t *testing.T) {
	m, _ := New(2, 10)
	for _, role := range []string{"boss", "r1", "r2"} {
		must(t, m.AddRole(role))
	}
	must(t, m.AddUser("bob"))
	must(t, m.AssignUser("bob", "boss"))
	must(t, m.AddSSD("s2", []string{"r1", "r2"}, 2))
	must(t, m.AddInherit("boss", "r1"))
	err := m.AddInherit("boss", "r2")
	assertKind(t, err, ErrViolation, "s2", "bob")
	if m.edgeExistsForTest("boss", "r2") {
		t.Fatal("rejected edge was stored")
	}
	if containsSet(m.authForTest("bob"), "r2") {
		t.Fatal("rejected edge changed auth")
	}
}

func TestConstraintCreationRejectedWhenCurrentDataViolates(t *testing.T) {
	m, _ := New(2, 10)
	for _, role := range []string{"a", "b", "x", "y"} {
		must(t, m.AddRole(role))
	}
	must(t, m.AddUser("u"))
	must(t, m.AssignUser("u", "a"))
	must(t, m.AssignUser("u", "b"))
	err := m.AddSSD("bad", []string{"a", "b"}, 2)
	assertKind(t, err, ErrViolation, "bad", "u")
	if _, ok := m.ssd["bad"]; ok {
		t.Fatal("invalid SSD was stored")
	}

	must(t, m.CreateSession("s", "u"))
	must(t, m.Activate("s", "a"))
	must(t, m.AddRole("c"))
	must(t, m.AssignUser("u", "c"))
	must(t, m.Activate("s", "c"))
	err = m.AddDSD("bad", []string{"a", "c"}, 2)
	assertKind(t, err, ErrViolation, "bad", "s")
	if _, ok := m.dsd["bad"]; ok {
		t.Fatal("invalid DSD was stored")
	}
}

func TestSSDAndDSDNamespacesIndependent(t *testing.T) {
	m, _ := New(2, 10)
	must(t, m.AddRole("a"))
	must(t, m.AddRole("b"))
	must(t, m.AddSSD("same", []string{"a", "b"}, 2))
	must(t, m.AddDSD("same", []string{"a", "b"}, 2))
	err := m.AddSSD("same", []string{"a", "b"}, 2)
	if !IsConflict(err) {
		t.Fatalf("second SSD got %v", err)
	}
	err = m.AddDSD("same", []string{"a", "b"}, 2)
	if !IsConflict(err) {
		t.Fatalf("second DSD got %v", err)
	}
}

func TestImplicitActivationAndExplicitRedundantActivation(t *testing.T) {
	m, _ := New(2, 10)
	for _, role := range []string{"lead", "dev", "ops"} {
		must(t, m.AddRole(role))
	}
	must(t, m.AddInherit("lead", "dev"))
	must(t, m.AddUser("carol"))
	must(t, m.AssignUser("carol", "lead"))
	must(t, m.AssignUser("carol", "ops"))
	must(t, m.AddDSD("d1", []string{"dev", "ops"}, 2))
	must(t, m.CreateSession("sc", "carol"))
	must(t, m.Activate("sc", "lead"))

	err := m.Activate("sc", "ops")
	assertKind(t, err, ErrViolation, "d1", "sc")
	err = m.Deactivate("sc", "dev")
	e, ok := err.(*RBACError)
	if !ok || e.Kind != ErrConflict || !reflect.DeepEqual(e.ImplicitBy, []string{"lead"}) {
		t.Fatalf("got %#v, want implicit by lead", err)
	}
	must(t, m.Activate("sc", "dev"))
	if got := m.activeForTest("sc"); !reflect.DeepEqual(got, map[string]struct{}{"lead": {}, "dev": {}}) {
		t.Fatalf("active = %v", got)
	}
	must(t, m.Deactivate("sc", "lead"))
	if got := m.activeForTest("sc"); !reflect.DeepEqual(got, map[string]struct{}{"dev": {}}) {
		t.Fatalf("active = %v", got)
	}
	if got := m.effectiveForTest("sc"); !reflect.DeepEqual(got, map[string]struct{}{"dev": {}}) {
		t.Fatalf("eff = %v", got)
	}
}

func TestUnauthorizedAndDuplicateActivationDistinguished(t *testing.T) {
	m, _ := New(2, 10)
	for _, role := range []string{"a", "b"} {
		must(t, m.AddRole(role))
	}
	must(t, m.AddUser("u"))
	must(t, m.CreateSession("s", "u"))
	must(t, m.AssignUser("u", "a"))
	must(t, m.Activate("s", "a"))
	if err := m.Activate("s", "a"); !IsConflict(err) {
		t.Fatalf("duplicate got %v", err)
	}
	if err := m.Activate("s", "b"); !IsConflict(err) {
		t.Fatalf("unauthorized got %v", err)
	}
}

func TestCascadeDeactivation(t *testing.T) {
	m, _ := New(3, 10)
	for _, role := range []string{"lead", "dev"} {
		must(t, m.AddRole(role))
	}
	must(t, m.AddInherit("lead", "dev"))
	must(t, m.AddUser("dave"))
	must(t, m.AssignUser("dave", "lead"))
	must(t, m.AssignUser("dave", "dev"))
	must(t, m.CreateSession("s1", "dave"))
	must(t, m.CreateSession("s0", "dave"))
	must(t, m.Activate("s1", "lead"))
	must(t, m.Activate("s1", "dev"))
	must(t, m.Activate("s0", "dev"))

	removed, err := m.DeassignUser("dave", "dev")
	must(t, err)
	if len(removed) != 0 {
		t.Fatalf("removed = %v", removed)
	}
	removed, err = m.DeleteInherit("lead", "dev")
	must(t, err)
	want := []InactiveAssignment{{Session: "s0", Role: "dev"}, {Session: "s1", Role: "dev"}}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	if got := m.activeForTest("s1"); !reflect.DeepEqual(got, map[string]struct{}{"lead": {}}) {
		t.Fatalf("active s1 = %v", got)
	}
	if got := m.effectiveForTest("s1"); !reflect.DeepEqual(got, map[string]struct{}{"lead": {}}) {
		t.Fatalf("eff s1 = %v", got)
	}
}

func TestSessionLimit(t *testing.T) {
	m, _ := New(1, 10)
	must(t, m.AddUser("u"))
	must(t, m.CreateSession("a", "u"))
	err := m.CreateSession("b", "u")
	if !IsLimitExceeded(err) {
		t.Fatalf("got %v, want limit", err)
	}
	if _, ok := m.sessions["b"]; ok {
		t.Fatal("rejected session was stored")
	}
}

func TestPermissionCheckThroughEffectiveRoles(t *testing.T) {
	m, _ := New(1, 10)
	must(t, m.AddRole("lead"))
	must(t, m.AddRole("dev"))
	must(t, m.AddInherit("lead", "dev"))
	must(t, m.AddUser("u"))
	must(t, m.AssignUser("u", "lead"))
	must(t, m.AssignPerm("dev", "code"))
	must(t, m.CreateSession("s", "u"))
	ok, err := m.Check("s", "code")
	must(t, err)
	if ok {
		t.Fatal("permission available before activation")
	}
	must(t, m.Activate("s", "lead"))
	ok, err = m.Check("s", "code")
	must(t, err)
	if !ok {
		t.Fatal("permission unavailable through inherited role")
	}
}

func TestInheritCountsIgnoreUnrelatedUsersAndSessions(t *testing.T) {
	m, _ := New(1000, 1000)
	for _, role := range []string{"boss", "r1", "r2", "other"} {
		must(t, m.AddRole(role))
	}
	for i := 0; i < 10000; i++ {
		user := fmt.Sprintf("noise-user-%d", i)
		must(t, m.AddUser(user))
		must(t, m.AssignUser(user, "other"))
	}
	for _, user := range []string{"u1", "u2", "u3"} {
		must(t, m.AddUser(user))
		must(t, m.AssignUser(user, "boss"))
	}
	for i := 0; i < 10000; i++ {
		sid := fmt.Sprintf("noise-session-%d", i)
		must(t, m.CreateSession(sid, fmt.Sprintf("noise-user-%d", i)))
		must(t, m.Activate(sid, "other"))
	}
	must(t, m.CreateSession("s1", "u1"))
	must(t, m.CreateSession("s2", "u2"))
	must(t, m.Activate("s1", "boss"))
	must(t, m.Activate("s2", "boss"))

	m.resetInheritCounts()
	must(t, m.AddInherit("boss", "r1"))
	counts := m.lastInheritCounts()
	if counts.CheckedUsers != 3 || counts.CheckedSessions != 2 {
		t.Fatalf("add counts = %+v", counts)
	}
	if counts.RecomputedUsers != 0 || counts.RecomputedSessions != 0 {
		t.Fatalf("add should not expose recomputation counts: %+v", counts)
	}

	_, err := m.DeleteInherit("boss", "r1")
	must(t, err)
	counts = m.lastInheritCounts()
	if counts.RecomputedUsers != 3 || counts.RecomputedSessions != 2 {
		t.Fatalf("delete counts = %+v", counts)
	}
	if counts.CheckedUsers != 0 || counts.CheckedSessions != 0 {
		t.Fatalf("delete should expose recomputation counts only: %+v", counts)
	}
}
