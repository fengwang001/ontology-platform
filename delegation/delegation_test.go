package delegation

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	alice Subject = "alice"
	bob   Subject = "bob"
	carol Subject = "carol"
	dave  Subject = "dave"

	read  Permission = "read"
	write Permission = "write"
)

type clockHolder struct{ t time.Time }

func newTestManager(t *testing.T) (*Manager, *clockHolder, *strings.Builder) {
	t.Helper()
	clk := &clockHolder{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	var logs strings.Builder
	var logMu sync.Mutex
	m := NewManager(map[Subject][]Permission{alice: []Permission{read, write}}).
		WithClock(func() time.Time { return clk.t }).
		WithLogger(func(format string, args ...any) {
			logMu.Lock()
			defer logMu.Unlock()
			fmt.Fprintf(&logs, format+"\n", args...)
		})
	t.Cleanup(func() {
		t.Logf("delegation logs:\n%s", logs.String())
	})
	return m, clk, &logs
}

func edge(id string, from, to Subject, p Permission, expiry time.Time, can bool) Edge {
	return Edge{
		ID:            id,
		Delegator:     from,
		Delegatee:     to,
		Permission:    p,
		ExpiresAt:     expiry,
		CanRedelegate: can,
	}
}

func mustRegister(t *testing.T, m *Manager, e Edge) {
	t.Helper()
	if err := m.Register(e); err != nil {
		t.Fatalf("register %s: %v", e.ID, err)
	}
}

func TestSkeleton(t *testing.T) {
	m, _, _ := newTestManager(t)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
}

func TestDelegationTransfer(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)

	if err := m.Register(edge("d1", alice, bob, read, expiry, true)); err != nil {
		t.Fatalf("register d1: %v", err)
	}

	ok, basis := m.HasPermission(bob, read)
	if !ok {
		t.Fatalf("bob should hold read, basis=%s", basis)
	}
	if !strings.Contains(basis, "DELEGATION_CHAIN") || !strings.Contains(basis, "d1") {
		t.Fatalf("basis should cite delegation chain d1, got %q", basis)
	}

	if ok, basis = m.HasPermission(bob, write); ok {
		t.Fatalf("bob must not hold write, basis=%s", basis)
	}
}

func TestRedelegateAllowedByFlag(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)

	mustRegister(t, m, edge("d1", alice, bob, read, expiry, true))
	mustRegister(t, m, edge("d2", bob, carol, read, expiry, true))
	mustRegister(t, m, edge("d3", carol, dave, read, expiry, false))

	for _, s := range []Subject{bob, carol, dave} {
		if ok, basis := m.HasPermission(s, read); !ok {
			t.Fatalf("%s should hold read: %s", s, basis)
		}
	}

	err := m.Register(edge("d4", dave, Subject("erin"), read, expiry, true))
	if err == nil {
		t.Fatal("redelegation by dave must be rejected")
	}
	if rej, ok := AsReject(err); !ok || rej.Reason != ReasonRedelegateForbidden {
		t.Fatalf("want REDELEGATE_FORBIDDEN, got %v", err)
	}
	if _, exists := m.edges["d4"]; exists {
		t.Fatal("rejected delegation must not be stored")
	}
}

func TestRedelegateForbiddenFlag(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)

	mustRegister(t, m, edge("d1", alice, bob, write, expiry, false))
	err := m.Register(edge("d2", bob, carol, write, expiry, true))
	if err == nil {
		t.Fatal("d2 must be rejected: d1 forbids redelegation")
	}
	if rej, ok := AsReject(err); !ok || rej.Reason != ReasonRedelegateForbidden {
		t.Fatalf("want REDELEGATE_FORBIDDEN, got %v", err)
	}
	if ok, _ := m.HasPermission(carol, write); ok {
		t.Fatal("carol must not hold write after rejected redelegation")
	}
}

func TestNoAuthorityRejected(t *testing.T) {
	m, clk, _ := newTestManager(t)
	err := m.Register(edge("x", carol, bob, read, clk.t.Add(time.Hour), true))
	if err == nil {
		t.Fatal("carol has no read, delegation must fail")
	}
	if rej, ok := AsReject(err); !ok || rej.Reason != ReasonNoAuthority {
		t.Fatalf("want NO_AUTHORITY, got %v", err)
	}
}

func TestRevokeCascadesDownstream(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)

	mustRegister(t, m, edge("d1", alice, bob, read, expiry, true))
	mustRegister(t, m, edge("d2", bob, carol, read, expiry, true))
	mustRegister(t, m, edge("d3", carol, dave, read, expiry, true))

	if err := m.Revoke("d1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	for _, s := range []Subject{bob, carol, dave} {
		ok, basis := m.HasPermission(s, read)
		if ok {
			t.Fatalf("%s must lose read after upstream revoke, basis=%s", s, basis)
		}
		if s != bob && !strings.Contains(basis, "REVOKED") {
			t.Fatalf("basis should indicate revoked chain, got %q", basis)
		}
	}

	err := m.Register(edge("d4", bob, carol, read, expiry, true))
	if err == nil || !IsReject(err, ReasonNoAuthority) {
		t.Fatalf("post-revoke register must fail NO_AUTHORITY, got %v", err)
	}
}

func TestRevokeMiddleEdgeCascadesOnlyDownstream(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)

	mustRegister(t, m, edge("d1", alice, bob, read, expiry, true))
	mustRegister(t, m, edge("d2", bob, carol, read, expiry, true))
	mustRegister(t, m, edge("d3", carol, dave, read, expiry, true))

	if err := m.Revoke("d2"); err != nil {
		t.Fatalf("revoke d2: %v", err)
	}
	if ok, _ := m.HasPermission(bob, read); !ok {
		t.Fatal("bob must retain read when middle edge d2 is revoked")
	}
	if ok, basis := m.HasPermission(carol, read); ok {
		t.Fatalf("carol must lose read: %s", basis)
	}
	if ok, basis := m.HasPermission(dave, read); ok {
		t.Fatalf("dave must lose read: %s", basis)
	}
}

func TestCycleRejected(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)

	mustRegister(t, m, edge("d1", alice, bob, read, expiry, true))
	mustRegister(t, m, edge("d2", bob, carol, read, expiry, true))

	// carol -> alice：alice 以根身份持有 read，注册后将形成 carol→alice→bob→carol 环。
	err := m.Register(edge("d3", carol, alice, read, expiry, true))
	if err == nil {
		t.Fatal("cycle-forming delegation must be rejected")
	}
	if rej, ok := AsReject(err); !ok || rej.Reason != ReasonCycle {
		t.Fatalf("want CYCLE_DETECTED, got %v", err)
	}
	if _, exists := m.edges["d3"]; exists {
		t.Fatal("cyclic delegation must not be stored")
	}
}

func TestSelfDelegationRejected(t *testing.T) {
	m, clk, _ := newTestManager(t)
	err := m.Register(edge("self", alice, alice, read, clk.t.Add(time.Hour), true))
	if err == nil || !IsReject(err, ReasonSelfDelegation) {
		t.Fatalf("self delegation must be rejected, got %v", err)
	}
}

func TestExpiry(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)
	mustRegister(t, m, edge("d1", alice, bob, read, expiry, true))
	mustRegister(t, m, edge("d2", bob, carol, read, expiry.Add(time.Hour), true))

	clk.t = expiry.Add(time.Second)
	if ok, basis := m.HasPermission(bob, read); ok {
		t.Fatalf("bob read must expire: %s", basis)
	}
	if ok, basis := m.HasPermission(carol, read); ok {
		t.Fatalf("carol read must expire with upstream: %s", basis)
	}

	err := m.Register(edge("d3", bob, dave, read, clk.t.Add(time.Hour), true))
	if err == nil || !IsReject(err, ReasonNoAuthority) {
		t.Fatalf("expired holder cannot redelegate, got %v", err)
	}

	err = m.Register(edge("d4", alice, dave, read, clk.t.Add(-time.Minute), true))
	if err == nil || !IsReject(err, ReasonExpired) {
		t.Fatalf("past expiry must be rejected ALREADY_EXPIRED, got %v", err)
	}
}

func TestRegisterSetOrderIndependence(t *testing.T) {
	expiry := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mkSet := func() []Edge {
		return []Edge{
			edge("d1", alice, bob, read, expiry, true),
			edge("d2", bob, carol, read, expiry, true),
			edge("d3", carol, dave, read, expiry, false),
		}
	}

	perms := func(m *Manager) map[string]bool {
		out := map[string]bool{}
		for _, s := range []Subject{alice, bob, carol, dave} {
			for p := range m.EffectivePermissions(s) {
				out[string(s)+":"+string(p)] = true
			}
		}
		return out
	}

	m1, clk1, _ := newTestManager(t)
	clk1.t = expiry.Add(-time.Hour)
	if err := m1.RegisterSet(mkSet()); err != nil {
		t.Fatalf("forward order: %v", err)
	}

	m2, clk2, _ := newTestManager(t)
	clk2.t = expiry.Add(-time.Hour)
	reversed := mkSet()
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if err := m2.RegisterSet(reversed); err != nil {
		t.Fatalf("reverse order: %v", err)
	}

	p1, p2 := perms(m1), perms(m2)
	if fmt.Sprint(p1) != fmt.Sprint(p2) {
		t.Fatalf("effective permissions differ by registration order:\n%v\n%v", p1, p2)
	}
	if !p1["bob:read"] || !p1["carol:read"] || !p1["dave:read"] {
		t.Fatalf("expected full chain, got %v", p1)
	}
}

func TestRegisterSetRejectsForbiddenRedelegate(t *testing.T) {
	expiry := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	m, clk, _ := newTestManager(t)
	clk.t = expiry.Add(-time.Hour)
	set := []Edge{
		edge("d1", alice, bob, read, expiry, false),
		edge("d2", bob, carol, read, expiry, true),
	}
	err := m.RegisterSet(set)
	if err == nil || !IsReject(err, ReasonRedelegateForbidden) {
		t.Fatalf("batch must reject forbidden redelegation, got %v", err)
	}
	if _, exists := m.edges["d1"]; exists {
		t.Fatal("rejected batch must not commit any edge")
	}
}

func TestRegisterSetRejectsCycle(t *testing.T) {
	expiry := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	m, clk, _ := newTestManager(t)
	clk.t = expiry.Add(-time.Hour)
	set := []Edge{
		edge("d1", alice, bob, read, expiry, true),
		edge("d2", bob, carol, read, expiry, true),
		edge("d3", carol, alice, read, expiry, true),
	}
	err := m.RegisterSet(set)
	if err == nil || !IsReject(err, ReasonCycle) {
		t.Fatalf("batch must reject cycle, got %v", err)
	}
	if len(m.edges) != 0 {
		t.Fatalf("rejected batch must roll back, edges=%v", m.edges)
	}
}

func TestRejectedOperationDoesNotMutate(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)
	mustRegister(t, m, edge("d1", alice, bob, read, expiry, false))

	before := map[string]bool{}
	for id := range m.edges {
		before[id] = true
	}

	err := m.Register(edge("d2", bob, carol, read, expiry, true))
	if !IsReject(err, ReasonRedelegateForbidden) {
		t.Fatalf("want REDELEGATE_FORBIDDEN, got %v", err)
	}

	after := map[string]bool{}
	for id := range m.edges {
		after[id] = true
	}
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("state changed after rejection: before=%v after=%v", before, after)
	}
	if ok, basis := m.HasPermission(bob, read); !ok {
		t.Fatalf("bob permission must remain intact: %s", basis)
	}
}

func TestConcurrentRegistrationAndEvaluation(t *testing.T) {
	m, clk, _ := newTestManager(t)
	expiry := clk.t.Add(time.Hour)

	const n = 50
	var wg sync.WaitGroup

	// 每个 goroutine 使用独立的根权限/目标主体，避免同权限多来源带来的非确定性，
	// 从而在任意交错下最终注册结果都必须稳定一致。
	perms := []Permission{"p0", "p1", "p2", "p3", "p4"}

	for i := 0; i < n; i++ {
		p := perms[i%len(perms)]
		m.GrantRoot(alice, p)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("c%d", i)
			from := Subject(fmt.Sprintf("root-%d", i))
			if i%2 == 0 {
				from = alice
			} else {
				m.GrantRoot(from, p)
			}
			_ = m.Register(edge(id, from, Subject(fmt.Sprintf("u%d", i)), p, expiry, true))
		}(i)
	}

	for i := 0; i < n; i++ {
		p := perms[i%len(perms)]
		wg.Add(1)
		go func(p Permission) {
			defer wg.Done()
			_, _ = m.HasPermission(bob, p)
			_ = m.EffectivePermissions(alice)
		}(p)
	}

	wg.Wait()

	var accepted int
	for i := 0; i < n; i++ {
		if _, ok := m.edges[fmt.Sprintf("c%d", i)]; ok {
			accepted++
		}
	}
	if accepted != n {
		t.Fatalf("all %d registrations should be accepted under concurrency, got %d", n, accepted)
	}
}

func TestLogsContainActorsPermissionAndBasis(t *testing.T) {
	m, clk, logs := newTestManager(t)
	expiry := clk.t.Add(time.Hour)
	mustRegister(t, m, edge("d1", alice, bob, read, expiry, true))
	_, _ = m.HasPermission(bob, read)

	out := logs.String()
	for _, want := range []string{"delegator=alice", "delegatee=bob", "permission=read", "decision=ALLOW", "basis="} {
		if !strings.Contains(out, want) {
			t.Fatalf("logs missing %q:\n%s", want, out)
		}
	}
}
