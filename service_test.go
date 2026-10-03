package ontology

import (
	"errors"
	"testing"

	"ontology/policy"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	s, err := New(policy.Policy{Dmax: 100, Rw: 30, Lk: 50, Cmax: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func setupExample(t *testing.T, s *Service) {
	t.Helper()
	for i, r := range []struct {
		name   string
		role   policy.Role
		scopes []string
	}{
		{"alice", policy.Engineer, nil},
		{"m1", policy.Manager, []string{"/db"}},
		{"m2", policy.Manager, []string{"/d"}},
		{"sec", policy.Security, nil},
	} {
		if err := s.Register(int64(i), r.name, r.role, r.scopes); err != nil {
			t.Fatalf("Register %s: %v", r.name, err)
		}
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func wantAccess(t *testing.T, s *Service, p, res string, at int64, want bool) {
	t.Helper()
	got, err := s.Access(p, res, at)
	if err != nil {
		t.Fatalf("Access(%s,%s,%d): %v", p, res, at, err)
	}
	if got != want {
		t.Fatalf("Access(%s,%s,%d) = %v, want %v", p, res, at, got, want)
	}
}

func TestSpecExampleApprove(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := s.Register(39, "bob", policy.Engineer, nil); err != nil { // 推进时钟
		t.Fatalf("Register: %v", err)
	}
	wantAccess(t, s, "alice", "/db/prod/users", 39, true)
	wantAccess(t, s, "alice", "/db/prod/users", 10, true)
	wantErr(t, s.Approve(39, "g1", "m2"), ErrPermission) // "/d" 不覆盖 "/db/prod"
	wantErr(t, s.Approve(39, "g1", "alice"), ErrSelfReview)
	if err := s.Approve(40, "g1", "m1"); err != nil { // 恰等截止，允许
		t.Fatalf("Approve at deadline: %v", err)
	}
	if err := s.Register(110, "carol", policy.Engineer, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	wantAccess(t, s, "alice", "/db/prod", 39, true)
	wantAccess(t, s, "alice", "/db/prod", 40, true) // 批准后 effEnd=110
	wantAccess(t, s, "alice", "/db/prod", 109, true)
	wantAccess(t, s, "alice", "/db/prod", 110, false)
	wantErr(t, s.Approve(111, "g1", "m1"), ErrAlreadyDecided) // 已决先于逾期
	wantErr(t, s.Reject(112, "g1", "sec"), ErrAlreadyDecided)
}

func TestSpecExampleOverdue(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := s.Register(40, "bob", policy.Engineer, nil); err != nil { // 推进时钟
		t.Fatalf("Register: %v", err)
	}
	wantAccess(t, s, "alice", "/db/prod", 39, true)
	wantAccess(t, s, "alice", "/db/prod", 40, false) // 无评审 effEnd=40
	wantErr(t, s.Approve(41, "g1", "m1"), ErrOverdue)
}

func TestSpecExampleReject(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := s.Reject(25, "g1", "m1"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	wantAccess(t, s, "alice", "/db/prod", 24, true)
	wantAccess(t, s, "alice", "/db/prod", 25, false)                     // effEnd=25
	wantErr(t, s.Request(25, "g2", "alice", "/db/prod", 10), ErrLocked)  // 恰等 lockStart
	wantErr(t, s.Request(74, "g3", "alice", "/db/prod", 10), ErrLocked)  // lockStart+Lk-1
	if err := s.Request(75, "g4", "alice", "/db/prod", 10); err != nil { // 恰等 lockStart+Lk
		t.Fatalf("Request at lock end: %v", err)
	}
}

func TestUnreviewedLockWindow(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 10); err != nil {
		t.Fatalf("Request: %v", err)
	}
	// 无评审：lockStart = 10+30 = 40，锁定区间 [40, 90)
	wantErr(t, s.Request(40, "g2", "alice", "/db/prod", 10), ErrLocked)
	wantErr(t, s.Request(89, "g3", "alice", "/db/prod", 10), ErrLocked)
	if err := s.Request(90, "g4", "alice", "/db/prod", 10); err != nil {
		t.Fatalf("Request at lock end: %v", err)
	}
}

func TestRejectAfterNominalEnd(t *testing.T) {
	s, err := New(policy.Policy{Dmax: 100, Rw: 200, Lk: 0, Cmax: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 50); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := s.Reject(70, "g1", "m1"); err != nil { // 晚于 nominalEnd=60
		t.Fatalf("Reject: %v", err)
	}
	wantAccess(t, s, "alice", "/db/prod", 59, true)
	wantAccess(t, s, "alice", "/db/prod", 60, false) // effEnd=min(60,70)=60
}

func TestPolicySnapshot(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request g1: %v", err)
	}
	p := policy.Policy{Dmax: 100, Rw: 10, Lk: 50, Cmax: 3}
	if err := s.SetPolicy(20, p); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if err := s.Request(25, "g2", "alice", "/db/prod", 50); err != nil {
		t.Fatalf("Request g2: %v", err)
	}
	if err := s.Register(39, "bob", policy.Engineer, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// g1 仍按快照 Rw=30：effEnd=40；g2 按新策略 Rw=10：effEnd=35
	wantAccess(t, s, "alice", "/db/prod", 36, true) // 仅 g1 有效（g2 已失效）
	wantAccess(t, s, "alice", "/db/prod", 39, true)
	entries := s.Ledger(0)
	if entries[len(entries)-2].PolicyVer != 2 { // g2 的 Request 记录版本 2
		t.Fatalf("g2 PolicyVer = %d, want 2", entries[len(entries)-2].PolicyVer)
	}
}

func TestRequestRejectionOrder(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	wantErr(t, s.Request(9, "g2", "alice", "bad", 10), ErrInvalidParam) // 参数先于时钟
	wantErr(t, s.Request(9, "g2", "alice", "/db/prod", 10), ErrClockRegression)
	wantErr(t, s.Request(11, "g1", "alice", "/db/prod", 10), ErrDuplicate)
	wantErr(t, s.Request(11, "g2", "ghost", "/db/prod", 10), ErrNotRegistered)
	wantErr(t, s.Request(11, "g2", "alice", "/db/prod", 101), ErrInvalidParam) // D > Dmax
	// Cmax=3：g1 在 [10,40) 有效，再叠加两个后达到上限
	if err := s.Request(12, "g2", "alice", "/db", 10); err != nil {
		t.Fatalf("Request g2: %v", err)
	}
	if err := s.Request(13, "g3", "alice", "/db", 10); err != nil {
		t.Fatalf("Request g3: %v", err)
	}
	wantErr(t, s.Request(14, "g4", "alice", "/db", 10), ErrTooManyConcurrent)
}
