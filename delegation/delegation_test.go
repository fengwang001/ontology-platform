package delegation

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

const permRead = Permission("read")

type memLogger struct {
	mu     sync.Mutex
	events []string
}

func (m *memLogger) LogGrant(ctx context.Context, d *Delegation, decision string, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, fmt.Sprintf("grant delegator=%s delegatee=%s permission=%s decision=%s basis=%s",
		d.Delegator, d.Delegatee, d.Permission, decision, reason))
}

func (m *memLogger) LogRevoke(ctx context.Context, d *Delegation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, fmt.Sprintf("revoke delegator=%s delegatee=%s permission=%s",
		d.Delegator, d.Delegatee, d.Permission))
}

func (m *memLogger) LogEvaluate(ctx context.Context, subject Principal, perm Permission, allowed bool, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, fmt.Sprintf("evaluate subject=%s permission=%s allowed=%t basis=%s",
		subject, perm, allowed, reason))
}

func (m *memLogger) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...)
}

func newTestService(now time.Time) (*Service, *memLogger) {
	lg := &memLogger{}
	s := NewService(lg, func() time.Time { return now }, map[Principal]map[Permission]bool{
		"alice": {permRead: true},
	})
	return s, lg
}

func grant(t *testing.T, s *Service, from, to Principal, perm Permission, canDelegate bool, exp time.Time) *Delegation {
	t.Helper()
	d, err := s.Grant(context.Background(), GrantRequest{
		Delegator: from, Delegatee: to, Permission: perm, ExpiresAt: exp, CanDelegate: canDelegate,
	})
	if err != nil {
		t.Fatalf("unexpected grant error %s -> %s: %v", from, to, err)
	}
	return d
}

func grantExpect(t *testing.T, s *Service, from, to Principal, perm Permission, want RejectReason) {
	t.Helper()
	_, err := s.Grant(context.Background(), GrantRequest{
		Delegator: from, Delegatee: to, Permission: perm, CanDelegate: true,
	})
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("grant %s -> %s: want *RejectError, got %v", from, to, err)
	}
	if re.Reason != want {
		t.Fatalf("grant %s -> %s: want reason %s, got %s (%v)", from, to, want, re.Reason, err)
	}
}

// 1. 委托沿有效链向下传递；日志含委托者、受托者、权限与判定依据。
func TestDelegationTransitive(t *testing.T) {
	s, lg := newTestService(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()

	if d := s.HasPermission(ctx, "alice", permRead); !d.Allowed || d.Reason != ReasonRootAuthority {
		t.Fatalf("alice root: %+v", d)
	}

	d1 := grant(t, s, "alice", "bob", permRead, true, time.Time{})
	grant(t, s, "bob", "carol", permRead, true, time.Time{})

	if d := s.HasPermission(ctx, "bob", permRead); !d.Allowed || d.Reason != ReasonDelegated {
		t.Fatalf("bob: %+v", d)
	}
	if d := s.HasPermission(ctx, "carol", permRead); !d.Allowed {
		t.Fatalf("carol should inherit via chain: %+v", d)
	}
	if d := s.HasPermission(ctx, "dave", permRead); d.Allowed || d.Reason != ReasonNoDelegation {
		t.Fatalf("dave unrelated: %+v", d)
	}
	if d := s.HasPermission(ctx, "bob", Permission("write")); d.Allowed {
		t.Fatal("permissions must not leak across permission types")
	}
	if d1.ID == 0 {
		t.Fatal("delegation must have an ID")
	}

	var sawGrant, sawEval bool
	for _, ev := range lg.snapshot() {
		if strings.Contains(ev, "grant delegator=alice delegatee=bob permission=read") &&
			strings.Contains(ev, "basis=") {
			sawGrant = true
		}
		if strings.Contains(ev, "evaluate subject=carol permission=read allowed=true basis=") {
			sawEval = true
		}
	}
	if !sawGrant || !sawEval {
		t.Fatalf("audit log missing required fields: %v", lg.snapshot())
	}
}

// 2. CanDelegate=false 的权限被再委托必须拒绝，拒绝不改变状态。
func TestRedelegationFlag(t *testing.T) {
	s, _ := newTestService(time.Now())
	ctx := context.Background()

	grant(t, s, "alice", "bob", permRead, false, time.Time{})

	if d := s.CanDelegate(ctx, "bob", permRead); d.Allowed || d.Reason != ReasonNotForwardable {
		t.Fatalf("bob must not forward: %+v", d)
	}
	before := len(s.ActiveDelegations())
	grantExpect(t, s, "bob", "carol", permRead, ReasonNotDelegatable)
	if got := len(s.ActiveDelegations()); got != before {
		t.Fatalf("rejected grant mutated state: %d edges before, %d after", before, got)
	}
	if d := s.HasPermission(ctx, "carol", permRead); d.Allowed {
		t.Fatal("carol must not gain permission from a rejected grant")
	}

	grant(t, s, "alice", "bob2", permRead, true, time.Time{})
	grant(t, s, "bob2", "carol2", permRead, false, time.Time{})
	if d := s.HasPermission(ctx, "carol2", permRead); !d.Allowed {
		t.Fatalf("carol2 holds permission even if non-forwardable: %+v", d)
	}
	grantExpect(t, s, "carol2", "dave2", permRead, ReasonNotDelegatable)
}

// 3. 撤销源头：所有下游链式失效，旁支不受影响。
func TestRevocationCascades(t *testing.T) {
	s, _ := newTestService(time.Now())
	ctx := context.Background()

	ab := grant(t, s, "alice", "bob", permRead, true, time.Time{})
	bc := grant(t, s, "bob", "carol", permRead, true, time.Time{})
	cd := grant(t, s, "carol", "dave", permRead, true, time.Time{})
	grant(t, s, "alice", "erin", permRead, true, time.Time{})

	if err := s.Revoke(ctx, ab.ID); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"bob", "carol", "dave"} {
		if d := s.HasPermission(ctx, Principal(name), permRead); d.Allowed || d.Reason != ReasonChainRevoked {
			t.Fatalf("%s must cascade-fail: %+v want %s", name, d, ReasonChainRevoked)
		}
	}
	if d := s.HasPermission(ctx, "erin", permRead); !d.Allowed {
		t.Fatalf("unrelated branch erin stays valid: %+v", d)
	}
	grantExpect(t, s, "dave", "frank", permRead, ReasonNotDelegatable)

	ids := map[int64]bool{}
	for _, e := range s.ActiveDelegations() {
		ids[e.ID] = true
	}
	for _, id := range []int64{ab.ID, bc.ID, cd.ID} {
		if ids[id] {
			t.Fatalf("revoked/cascaded edge %d still active", id)
		}
	}
	if err := s.Revoke(ctx, 9999); err == nil {
		t.Fatal("revoking unknown id must return an error")
	}
}

// 3b. 撤销中段节点：上游有效，自身与下游失效。
func TestRevocationMiddle(t *testing.T) {
	s, _ := newTestService(time.Now())
	ctx := context.Background()

	grant(t, s, "alice", "bob", permRead, true, time.Time{})
	bc := grant(t, s, "bob", "carol", permRead, true, time.Time{})
	grant(t, s, "carol", "dave", permRead, true, time.Time{})

	if err := s.Revoke(ctx, bc.ID); err != nil {
		t.Fatal(err)
	}
	if d := s.HasPermission(ctx, "bob", permRead); !d.Allowed {
		t.Fatalf("bob stays valid when an edge below him is revoked: %+v", d)
	}
	for _, name := range []string{"carol", "dave"} {
		if d := s.HasPermission(ctx, Principal(name), permRead); d.Allowed {
			t.Fatalf("%s must fail after mid-chain revoke: %+v", name, d)
		}
	}
}

// 4. 委托环拒绝；拒绝原因可区分；拒绝不改状态。
func TestCycleRejected(t *testing.T) {
	s, _ := newTestService(time.Now())

	grant(t, s, "alice", "bob", permRead, true, time.Time{})
	grant(t, s, "bob", "carol", permRead, true, time.Time{})
	before := len(s.ActiveDelegations())

	grantExpect(t, s, "carol", "alice", permRead, ReasonCycle)
	grantExpect(t, s, "carol", "bob", permRead, ReasonCycle)
	grantExpect(t, s, "bob", "bob", permRead, ReasonCycle)

	if got := len(s.ActiveDelegations()); got != before {
		t.Fatalf("rejected cycle edges mutated state: %d before, %d after", before, got)
	}

	if _, err := s.Grant(context.Background(), GrantRequest{
		Delegator: "carol", Delegatee: "alice", Permission: Permission("write"), CanDelegate: true,
	}); err != nil {
		t.Fatalf("cross-permission edge must not be seen as a cycle: %v", err)
	}
	if ReasonCycle.String() == ReasonNotDelegatable.String() {
		t.Fatal("reject reasons must be distinguishable")
	}
}

// 5. 有效期：过期边使整条链失效；存在未过期替代链时重新放行。
func TestExpiry(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lg := &memLogger{}
	clock := base
	s := NewService(lg, func() time.Time { return clock }, map[Principal]map[Permission]bool{
		"alice": {permRead: true},
	})
	ctx := context.Background()

	grant(t, s, "alice", "bob", permRead, true, base.Add(time.Hour))
	grant(t, s, "bob", "carol", permRead, true, base.Add(48*time.Hour))

	clock = base.Add(30 * time.Minute)
	if d := s.HasPermission(ctx, "carol", permRead); !d.Allowed {
		t.Fatalf("carol valid before expiry: %+v", d)
	}

	clock = base.Add(2 * time.Hour)
	if d := s.HasPermission(ctx, "bob", permRead); d.Allowed || d.Reason != ReasonChainExpired {
		t.Fatalf("bob expired: %+v", d)
	}
	if d := s.HasPermission(ctx, "carol", permRead); d.Allowed || d.Reason != ReasonChainExpired {
		t.Fatalf("carol chain expired through bob: %+v", d)
	}
	grantExpect(t, s, "carol", "dave", permRead, ReasonNotDelegatable)

	grant(t, s, "alice", "bob2", permRead, true, base.Add(10*time.Hour))
	grant(t, s, "bob2", "carol", permRead, true, base.Add(10*time.Hour))
	if d := s.HasPermission(ctx, "carol", permRead); !d.Allowed {
		t.Fatalf("fresh alternate path must re-grant carol: %+v", d)
	}
}

// 6. 并发：并发注册与求值无数据竞争，最终线性链完整连通。
func TestConcurrentGrantsAndEvaluations(t *testing.T) {
	s, lg := newTestService(time.Now())
	ctx := context.Background()

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		i := i
		go func() {
			defer wg.Done()
			from := Principal("alice")
			if i > 0 {
				from = Principal(fmt.Sprintf("p%d", i-1))
			}
			if _, err := s.Grant(ctx, GrantRequest{
				Delegator: from, Delegatee: Principal(fmt.Sprintf("p%d", i)),
				Permission: permRead, CanDelegate: true,
			}); err != nil {
				t.Errorf("concurrent grant i=%d: %v", i, err)
			}
		}()
		go func() {
			defer wg.Done()
			_ = s.HasPermission(ctx, Principal(fmt.Sprintf("p%d", i%5)), permRead)
		}()
	}
	wg.Wait()

	if got := len(s.ActiveDelegations()); got != n {
		t.Fatalf("all %d linear-chain grants must register regardless of scheduling, got %d", n, got)
	}
	if d := s.HasPermission(ctx, Principal(fmt.Sprintf("p%d", n-1)), permRead); !d.Allowed {
		t.Fatalf("tail of concurrently registered chain must resolve: %+v", d)
	}
	if len(lg.snapshot()) == 0 {
		t.Fatal("audit log must not be empty under concurrent use")
	}
}

// 7. 顺序无关：同一组无环委托以任意顺序注册，最终权限完全相同。
func TestOrderIndependence(t *testing.T) {
	type edge struct{ from, to Principal }
	edges := []edge{
		{"bob", "carol"},
		{"alice", "bob"},
		{"carol", "dave"},
		{"alice", "erin"},
		{"erin", "frank"},
	}
	subjects := []Principal{"bob", "carol", "dave", "erin", "frank", "ghost"}

	build := func(order []int) map[Principal]bool {
		s, _ := newTestService(time.Now())
		ctx := context.Background()
		for _, idx := range order {
			e := edges[idx]
			grant(t, s, e.from, e.to, permRead, true, time.Time{})
		}
		out := map[Principal]bool{}
		for _, p := range subjects {
			out[p] = s.HasPermission(ctx, p, permRead).Allowed
		}
		return out
	}

	want := build([]int{0, 1, 2, 3, 4})
	for iter := 0; iter < 20; iter++ {
		order := rand.Perm(len(edges))
		got := build(order)
		for _, p := range subjects {
			if got[p] != want[p] {
				t.Fatalf("order %v gives %s allowed=%t, want %t", order, p, got[p], want[p])
			}
		}
	}

	// 期望的最终权限集合。
	for p, allowed := range map[Principal]bool{
		"bob": true, "carol": true, "dave": true, "erin": true, "frank": true, "ghost": false,
	} {
		if want[p] != allowed {
			t.Fatalf("final permission for %s = %t, want %t", p, want[p], allowed)
		}
	}
}

// 8. 悬空边在上游补齐后自动激活，但环检查始终先于激活生效。
func TestDanglingEdgeActivates(t *testing.T) {
	s, _ := newTestService(time.Now())
	ctx := context.Background()

	// bob 此时无权，carol 再下游也无环、无直接违例，边以悬空态注册。
	grant(t, s, "bob", "carol", permRead, true, time.Time{})
	if d := s.HasPermission(ctx, "carol", permRead); d.Allowed {
		t.Fatal("dangling edge must not grant permission before upstream exists")
	}
	grant(t, s, "alice", "bob", permRead, true, time.Time{})
	if d := s.HasPermission(ctx, "carol", permRead); !d.Allowed {
		t.Fatalf("dangling edge must activate after upstream registration: %+v", d)
	}
}

// 9. 拒绝原因可用 errors.Is/As 区分，且携带操作上下文。
func TestRejectErrorContent(t *testing.T) {
	s, _ := newTestService(time.Now())
	grant(t, s, "alice", "bob", permRead, false, time.Time{})
	_, err := s.Grant(context.Background(), GrantRequest{
		Delegator: "bob", Delegatee: "carol", Permission: permRead,
	})
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %T", err)
	}
	if re.Reason != ReasonNotDelegatable || re.Delegator != "bob" ||
		re.Delegatee != "carol" || re.Permission != permRead {
		t.Fatalf("reject error lost context: %+v", re)
	}
	if !strings.Contains(err.Error(), ReasonNotDelegatable.String()) {
		t.Fatalf("error message must carry distinguishable reason: %v", err)
	}
}
