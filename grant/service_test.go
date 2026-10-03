package grant

import (
	"testing"

	"ontology/policy"
)

func newExampleSvc(t *testing.T) *Service {
	t.Helper()
	svc, err := NewService(policy.Policy{Dmax: 100, Rw: 30, Lk: 50, Cmax: 3})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	regs := []struct {
		name   string
		role   policy.Role
		scopes []string
	}{
		{"alice", policy.RoleEngineer, nil},
		{"m1", policy.RoleManager, []string{"/db"}},
		{"m2", policy.RoleManager, []string{"/d"}},
		{"sec", policy.RoleSecurity, nil},
	}
	for _, r := range regs {
		if err := svc.Register(0, r.name, r.role, r.scopes); err != nil {
			t.Fatalf("Register %s: %v", r.name, err)
		}
	}
	return svc
}

func mustAccess(t *testing.T, svc *Service, who, res string, at int64, want bool) {
	t.Helper()
	got, err := svc.Access(who, res, at)
	if err != nil {
		t.Fatalf("Access(%s,%s,%d) err: %v", who, res, at, err)
	}
	t.Logf("Access(%s,%s,t=%d)=%v want=%v", who, res, at, got, want)
	if got != want {
		t.Fatalf("Access(%s,%s,%d)=%v, want %v", who, res, at, got, want)
	}
}

// 规格示例：先放行、恰等截止批准、权限与自评、已决优先于逾期。
func TestExampleApproveFlow(t *testing.T) {
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	g := svc.grants["g1"]
	t.Logf("g1: start=%d nominalEnd=%d Rw=%d Lk=%d ver=%d",
		g.Start, g.NominalEnd, g.Rw, g.Lk, g.PolicyVer)
	if g.Start != 10 || g.NominalEnd != 110 || g.Rw != 30 || g.PolicyVer != 1 {
		t.Fatalf("g1 snapshot wrong: %+v", g)
	}
	if err := svc.Approve(11, "g1", "m2"); err != ErrPermission {
		t.Fatalf("m2 approve = %v, want ErrPermission", err)
	}
	if err := svc.Approve(11, "g1", "alice"); err != ErrSelfReview {
		t.Fatalf("self approve = %v, want ErrSelfReview", err)
	}
	if err := svc.Register(40, "tmp", policy.RoleEngineer, nil); err != nil {
		t.Fatal(err)
	}
	mustAccess(t, svc, "alice", "/db/prod/users", 39, true)
	mustAccess(t, svc, "alice", "/db/prod/users", 40, false) // 无评审 effEnd=40
	if err := svc.Approve(40, "g1", "m1"); err != nil {
		t.Fatalf("m1 approve at deadline: %v", err)
	}
	if got := g.EffEnd(); got != 110 {
		t.Fatalf("approved effEnd=%d, want 110", got)
	}
	if err := svc.Approve(41, "g1", "m1"); err != ErrDecided {
		t.Fatalf("re-review = %v, want ErrDecided(先于逾期)", err)
	}
	if err := svc.Register(200, "bob", policy.RoleEngineer, nil); err != nil {
		t.Fatalf("Register bob: %v", err)
	}
	mustAccess(t, svc, "alice", "/db/prod", 109, true)
	mustAccess(t, svc, "alice", "/db/prod", 110, false)
}

// 评审恰等截止允许、差 1 逾期。
func TestReviewDeadline(t *testing.T) {
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(41, "g1", "m1"); err != ErrOverdue {
		t.Fatalf("approve@41 = %v, want ErrOverdue", err)
	}
	if err := svc.Approve(40, "g1", "sec"); err != nil {
		t.Fatalf("approve@40 = %v, want nil", err)
	}
}

// 驳回：effEnd=rejectedAt，锁定期 [lockStart, lockStart+Lk)。
func TestExampleRejectFlow(t *testing.T) {
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reject(25, "g1", "m1"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	g := svc.grants["g1"]
	if got := g.EffEnd(); got != 25 {
		t.Fatalf("rejected effEnd=%d, want 25", got)
	}
	mustAccess(t, svc, "alice", "/db/prod", 24, true)
	mustAccess(t, svc, "alice", "/db/prod", 25, false)
	if err := svc.Request(74, "g2", "alice", "/db/x", 10); err != ErrLocked {
		t.Fatalf("request@74 = %v, want ErrLocked(lockStart=25,Lk=50)", err)
	}
	if err := svc.Request(75, "g2", "alice", "/db/x", 10); err != nil {
		t.Fatalf("request@75 = %v, want nil(锁定恰结束)", err)
	}
	for id, g := range svc.grants {
		if !(g.Start <= g.EffEnd() && g.EffEnd() <= g.NominalEnd) {
			t.Fatalf("%s 违反 start<=effEnd<=nominalEnd: %+v", id, g)
		}
	}
}

// 策略快照：SetPolicy 前的授权保留旧 Rw，之后的新授权用新 Rw。
func TestPolicySnapshot(t *testing.T) {
	svc := newExampleSvc(t)
	if err := svc.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatal(err)
	}
	p2 := policy.Policy{Dmax: 100, Rw: 10, Lk: 50, Cmax: 3}
	if err := svc.SetPolicy(20, p2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Request(21, "g2", "alice", "/db/other", 100); err != nil {
		t.Fatal(err)
	}
	g1, g2 := svc.grants["g1"], svc.grants["g2"]
	t.Logf("g1.Rw=%d(快照30) g2.Rw=%d(新策略10)", g1.Rw, g2.Rw)
	if g1.Rw != 30 || g2.Rw != 10 || g2.PolicyVer != 2 {
		t.Fatalf("snapshot wrong: g1.Rw=%d g2.Rw=%d g2.ver=%d", g1.Rw, g2.Rw, g2.PolicyVer)
	}
	if err := svc.Register(40, "zed", policy.RoleEngineer, nil); err != nil {
		t.Fatal(err)
	}
	mustAccess(t, svc, "alice", "/db/prod", 39, true)  // g1 窗口仍到 40
	mustAccess(t, svc, "alice", "/db/prod", 40, false) // g1 无评审失效
	mustAccess(t, svc, "alice", "/db/other", 30, true)
	mustAccess(t, svc, "alice", "/db/other", 31, false) // g2 窗口到 31
}
