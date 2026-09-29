package rbac

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"testing"
)

func newTestManager(w io.Writer) *Manager {
	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return NewManager(logger)
}

func allowP(resource, action string) Permission {
	return Permission{Resource: resource, Action: action, Effect: Allow}
}

func denyP(resource, action string) Permission {
	return Permission{Resource: resource, Action: action, Effect: Deny}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMultiLevelInheritanceAndUnion(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("base"))
	mustOK(t, m.CreateRole("staff"))
	mustOK(t, m.CreateRole("admin"))

	mustOK(t, m.AddInheritance("staff", "base"))
	mustOK(t, m.AddInheritance("admin", "staff"))

	mustOK(t, m.GrantToRole("base", allowP("doc", "read")))
	mustOK(t, m.GrantToRole("staff", allowP("doc", "comment")))
	mustOK(t, m.GrantToRole("admin", allowP("doc", "delete")))

	mustOK(t, m.AssignRole("alice", "admin"))

	decision := m.Evaluate(context.Background(), "alice")
	for _, key := range []string{"doc:read", "doc:comment", "doc:delete"} {
		if !decision.Allowed[key] {
			t.Fatalf("expected %s allowed in %v", key, decision.Allowed)
		}
	}
	if decision.Sources["doc:read"] != "base" ||
		decision.Sources["doc:comment"] != "staff" ||
		decision.Sources["doc:delete"] != "admin" {
		t.Fatalf("unexpected sources: %v", decision.Sources)
	}
	if m.IsAllowed(context.Background(), "alice", "doc", "write") {
		t.Fatal("undefined permission must be denied")
	}
}

func TestMultipleRolesUnion(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("reader"))
	mustOK(t, m.CreateRole("writer"))
	mustOK(t, m.GrantToRole("reader", allowP("doc", "read")))
	mustOK(t, m.GrantToRole("writer", allowP("doc", "write")))
	mustOK(t, m.AssignRole("bob", "reader"))
	mustOK(t, m.AssignRole("bob", "writer"))

	decision := m.Evaluate(context.Background(), "bob")
	if !decision.Allowed["doc:read"] || !decision.Allowed["doc:write"] {
		t.Fatalf("expected union of both roles, got %v", decision.Allowed)
	}
}

func TestDirectGrantOverridesRole(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("reader"))
	mustOK(t, m.GrantToRole("reader", allowP("doc", "read")))
	mustOK(t, m.GrantToRole("reader", allowP("doc", "write")))
	mustOK(t, m.AssignRole("carol", "reader"))
	mustOK(t, m.GrantToSubject("carol", denyP("doc", "write")))

	decision := m.Evaluate(context.Background(), "carol")
	if !decision.Allowed["doc:read"] {
		t.Fatal("doc:read should stay allowed")
	}
	if decision.Allowed["doc:write"] {
		t.Fatal("direct deny must override role allow")
	}
	if decision.Sources["doc:write"] != "direct" {
		t.Fatalf("expected direct basis, got %q", decision.Sources["doc:write"])
	}

	mustOK(t, m.CreateRole("auditor"))
	mustOK(t, m.GrantToRole("auditor", denyP("log", "read")))
	mustOK(t, m.AssignRole("dave", "auditor"))
	mustOK(t, m.GrantToSubject("dave", allowP("log", "read")))
	if !m.IsAllowed(context.Background(), "dave", "log", "read") {
		t.Fatal("direct allow must override role deny")
	}
}

func TestRoleConflictDenyOverrides(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("a"))
	mustOK(t, m.CreateRole("b"))
	mustOK(t, m.AssignRole("eve", "a"))
	mustOK(t, m.AssignRole("eve", "b"))
	mustOK(t, m.GrantToRole("a", allowP("x", "y")))
	mustOK(t, m.GrantToRole("b", denyP("x", "y")))

	if m.IsAllowed(context.Background(), "eve", "x", "y") {
		t.Fatal("deny from one role must override allow from another")
	}
}

func TestCycleDetection(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("r1"))
	mustOK(t, m.CreateRole("r2"))
	mustOK(t, m.CreateRole("r3"))
	mustOK(t, m.AddInheritance("r1", "r2"))
	mustOK(t, m.AddInheritance("r2", "r3"))

	if err := m.AddInheritance("r3", "r1"); !errors.Is(err, ErrRoleCycle) {
		t.Fatalf("expected ErrRoleCycle, got %v", err)
	}
	if err := m.AddInheritance("r1", "r1"); !errors.Is(err, ErrRoleCycle) {
		t.Fatalf("self inheritance expected ErrRoleCycle, got %v", err)
	}
	if err := m.AddInheritance("r1", "r2"); !errors.Is(err, ErrDuplicateInherit) {
		t.Fatalf("duplicate edge expected ErrDuplicateInherit, got %v", err)
	}

	mustOK(t, m.CreateRole("r4"))
	mustOK(t, m.AddInheritance("r3", "r4"))
}

func TestMissingRolesAreRejected(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("real"))

	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"inherit missing child", func() error { return m.AddInheritance("ghost", "real") }, ErrRoleNotFound},
		{"inherit missing parent", func() error { return m.AddInheritance("real", "ghost") }, ErrRoleNotFound},
		{"assign missing role", func() error { return m.AssignRole("s", "ghost") }, ErrRoleNotFound},
		{"grant missing role", func() error { return m.GrantToRole("ghost", allowP("r", "a")) }, ErrRoleNotFound},
		{"revoke grant missing role", func() error { return m.RevokeFromRole("ghost", allowP("r", "a")) }, ErrRoleNotFound},
		{"revoke assignment missing role", func() error { return m.RevokeRole("s", "ghost") }, ErrRoleNotFound},
	}
	for _, tc := range cases {
		if err := tc.fn(); !errors.Is(err, tc.want) {
			t.Errorf("%s: expected %v, got %v", tc.name, tc.want, err)
		}
	}
	if err := m.CreateRole("real"); !errors.Is(err, ErrRoleExists) {
		t.Fatalf("duplicate role expected ErrRoleExists, got %v", err)
	}
}

func TestDuplicateAndMissingGrantErrors(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("r"))
	mustOK(t, m.AssignRole("s", "r"))

	mustOK(t, m.GrantToRole("r", allowP("x", "y")))
	if err := m.GrantToRole("r", allowP("x", "y")); !errors.Is(err, ErrDuplicateGrant) {
		t.Fatalf("expected ErrDuplicateGrant, got %v", err)
	}
	mustOK(t, m.GrantToSubject("s", allowP("x", "z")))
	if err := m.GrantToSubject("s", allowP("x", "z")); !errors.Is(err, ErrDuplicateGrant) {
		t.Fatalf("expected ErrDuplicateGrant, got %v", err)
	}
	if err := m.AssignRole("s", "r"); !errors.Is(err, ErrDuplicateAssignment) {
		t.Fatalf("expected ErrDuplicateAssignment, got %v", err)
	}

	if err := m.RevokeFromRole("r", allowP("nope", "y")); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("expected ErrGrantNotFound, got %v", err)
	}
	if err := m.RevokeFromRole("r", denyP("x", "y")); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("wrong-effect revoke expected ErrGrantNotFound, got %v", err)
	}
	if err := m.RevokeFromSubject("s", allowP("nope", "y")); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("expected ErrGrantNotFound, got %v", err)
	}
	mustOK(t, m.RevokeRole("s", "r"))
	if err := m.RevokeRole("s", "r"); !errors.Is(err, ErrAssignmentNotFound) {
		t.Fatalf("expected ErrAssignmentNotFound, got %v", err)
	}
}

func TestFailedOperationsAreAtomic(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("r1"))
	mustOK(t, m.CreateRole("r2"))
	mustOK(t, m.AddInheritance("r1", "r2"))

	_ = m.AddInheritance("r2", "r1")

	mustOK(t, m.GrantToRole("r2", allowP("doc", "read")))
	mustOK(t, m.AssignRole("u", "r1"))
	if !m.IsAllowed(context.Background(), "u", "doc", "read") {
		t.Fatal("rejected cycle edge altered the inheritance graph")
	}

	_ = m.GrantToRole("r2", allowP("doc", "read"))
	if err := m.RevokeFromSubject("u", allowP("doc", "read")); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("expected ErrGrantNotFound, got %v", err)
	}
	if !m.IsAllowed(context.Background(), "u", "doc", "read") {
		t.Fatal("revocation of missing direct grant mutated role-derived state")
	}
}

func TestRevokeTakesEffect(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("r"))
	mustOK(t, m.GrantToRole("r", allowP("doc", "read")))
	mustOK(t, m.AssignRole("s", "r"))
	mustOK(t, m.RevokeFromRole("r", allowP("doc", "read")))
	if m.IsAllowed(context.Background(), "s", "doc", "read") {
		t.Fatal("revoked role grant must no longer apply")
	}
}

func TestConcurrentEvaluationConsistency(t *testing.T) {
	m := newTestManager(io.Discard)
	mustOK(t, m.CreateRole("base"))
	mustOK(t, m.CreateRole("staff"))
	mustOK(t, m.CreateRole("admin"))
	mustOK(t, m.AddInheritance("staff", "base"))
	mustOK(t, m.AddInheritance("admin", "staff"))
	mustOK(t, m.GrantToRole("base", allowP("doc", "read")))
	mustOK(t, m.GrantToRole("staff", allowP("doc", "comment")))
	mustOK(t, m.GrantToRole("admin", denyP("doc", "delete")))
	mustOK(t, m.AssignRole("alice", "admin"))
	mustOK(t, m.GrantToSubject("alice", allowP("doc", "delete")))

	const goroutines = 32
	results := make([]Decision, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = m.Evaluate(context.Background(), "alice")
		}(i)
	}
	wg.Wait()

	for _, got := range results[1:] {
		if !reflectEqual(results[0], got) {
			t.Fatalf("concurrent evaluations differ:\n%v\n%v", results[0], got)
		}
	}
	d := results[0]
	if !d.Allowed["doc:read"] || !d.Allowed["doc:comment"] || !d.Allowed["doc:delete"] {
		t.Fatalf("unexpected decision: %v", d.Allowed)
	}
	if d.Sources["doc:delete"] != "direct" {
		t.Fatalf("direct override lost under concurrency: %v", d.Sources)
	}
}

func TestRegistrationOrderIndependence(t *testing.T) {
	type edge struct{ child, parent string }
	type roleGrant struct {
		role string
		p    Permission
	}

	build := func(seed int64) Decision {
		rng := rand.New(rand.NewSource(seed))
		m := newTestManager(io.Discard)

		roles := []string{"base", "staff", "admin", "auditor"}
		edges := []edge{
			{"staff", "base"},
			{"admin", "staff"},
			{"auditor", "base"},
		}
		grants := []roleGrant{
			{"base", allowP("doc", "read")},
			{"staff", allowP("doc", "comment")},
			{"admin", allowP("doc", "delete")},
			{"auditor", denyP("doc", "delete")},
		}

		rng.Shuffle(len(roles), func(i, j int) { roles[i], roles[j] = roles[j], roles[i] })
		for _, role := range roles {
			mustOK(t, m.CreateRole(role))
		}
		rng.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
		for _, e := range edges {
			mustOK(t, m.AddInheritance(e.child, e.parent))
		}
		rng.Shuffle(len(grants), func(i, j int) { grants[i], grants[j] = grants[j], grants[i] })
		for _, g := range grants {
			mustOK(t, m.GrantToRole(g.role, g.p))
		}
		mustOK(t, m.AssignRole("u", "admin"))
		mustOK(t, m.AssignRole("u", "auditor"))
		mustOK(t, m.GrantToSubject("u", allowP("doc", "share")))
		return m.Evaluate(context.Background(), "u")
	}

	baseline := build(1)
	for _, seed := range []int64{2, 3, 4, 5} {
		got := build(seed)
		if !reflectEqual(baseline, got) {
			t.Fatalf("order-dependent decision:\n%v\n%v", baseline, got)
		}
	}
	if baseline.Allowed["doc:delete"] {
		t.Fatal("auditor deny should win over admin allow regardless of order")
	}
	if !baseline.Allowed["doc:read"] || !baseline.Allowed["doc:comment"] || !baseline.Allowed["doc:share"] {
		t.Fatalf("unexpected union: %v", baseline.Allowed)
	}
}

func TestEvaluationLogsSubjectPermissionBasis(t *testing.T) {
	var buf bytes.Buffer
	m := newTestManager(&buf)
	mustOK(t, m.CreateRole("reader"))
	mustOK(t, m.GrantToRole("reader", allowP("doc", "read")))
	mustOK(t, m.AssignRole("frank", "reader"))
	mustOK(t, m.GrantToSubject("frank", denyP("doc", "share")))

	m.Evaluate(context.Background(), "frank")

	log := buf.String()
	for _, fragment := range []string{
		"subject=frank",
		`permission=doc:read`,
		`basis=reader`,
		`permission=doc:share`,
		`basis=direct`,
		`allowed=false`,
	} {
		if !bytes.Contains(buf.Bytes(), []byte(fragment)) {
			t.Fatalf("log missing %q, got:\n%s", fragment, log)
		}
	}
}

func reflectEqual(a, b Decision) bool {
	if a.Subject != b.Subject || len(a.Allowed) != len(b.Allowed) || len(a.Sources) != len(b.Sources) {
		return false
	}
	for key, value := range a.Allowed {
		if b.Allowed[key] != value {
			return false
		}
	}
	for key, value := range a.Sources {
		if b.Sources[key] != value {
			return false
		}
	}
	return true
}
