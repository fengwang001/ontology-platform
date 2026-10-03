package ontology

import (
	"fmt"
	"testing"

	"ontology/policy"
)

func TestAccessBounds(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := s.Access("alice", "/db/prod", -1); err != ErrFutureTime {
		t.Fatalf("t=-1 err = %v, want 未来时刻", err)
	}
	if _, err := s.Access("alice", "/db/prod", 11); err != ErrFutureTime { // 时钟+1
		t.Fatalf("t=clock+1 err = %v, want 未来时刻", err)
	}
	if _, err := s.Access("alice", "/db//prod", 10); err != ErrInvalidParam {
		t.Fatalf("bad resource err = %v, want 参数非法", err)
	}
	wantAccess(t, s, "alice", "/db/prod", 10, true) // t 恰等时钟
	got, err := s.Access("ghost", "/db/prod", 10)   // 未登记恒为假
	if err != nil || got {
		t.Fatalf("unregistered = %v, %v; want false, nil", got, err)
	}
}

func TestScopeSegmentBoundary(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/d", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	wantAccess(t, s, "alice", "/d", 10, true)        // 相等即覆盖
	wantAccess(t, s, "alice", "/d/x", 10, true)      // 段边界前缀
	wantAccess(t, s, "alice", "/db/prod", 10, false) // "/d" 不覆盖 "/db/prod"
	// m2 作用域 "/d" 可评审资源 "/d"，但不能评审 "/db/prod"
	if err := s.Approve(20, "g1", "m2"); err != nil {
		t.Fatalf("Approve by m2: %v", err)
	}
	if err := s.Request(21, "g2", "alice", "/db/prod", 10); err != nil {
		t.Fatalf("Request g2: %v", err)
	}
	wantErr(t, s.Approve(22, "g2", "m2"), ErrPermission)
	if err := s.Approve(22, "g2", "sec"); err != nil { // security 无作用域限制
		t.Fatalf("Approve by sec: %v", err)
	}
}

func countChecks(s *Service, p, res string, at int64) int64 {
	before := s.accessChecks
	_, _ = s.Access(p, res, at)
	return s.accessChecks - before
}

func TestAccessChecksOnlyOwnGrants(t *testing.T) {
	build := func(others int) *Service {
		s, err := New(policy.Policy{Dmax: 1_000_000_000, Rw: 1, Lk: 0, Cmax: 1_000_000_000})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := s.Register(0, "alice", policy.Engineer, nil); err != nil {
			t.Fatalf("Register alice: %v", err)
		}
		if err := s.Register(0, "bob", policy.Engineer, nil); err != nil {
			t.Fatalf("Register bob: %v", err)
		}
		if err := s.Request(0, "a1", "alice", "/x", 10); err != nil {
			t.Fatalf("Request a1: %v", err)
		}
		if err := s.Request(0, "a2", "alice", "/y", 10); err != nil {
			t.Fatalf("Request a2: %v", err)
		}
		for i := 0; i < others; i++ {
			if err := s.Request(0, fmt.Sprintf("b%d", i), "bob", "/z", 10); err != nil {
				t.Fatalf("Request b%d: %v", i, err)
			}
		}
		return s
	}
	s1 := build(1)
	s2 := build(5000)
	// 查询不命中任何授权，强制完整扫描 alice 自己的授权
	c1 := countChecks(s1, "alice", "/nowhere", 0)
	c2 := countChecks(s2, "alice", "/nowhere", 0)
	t.Logf("输入: alice 2 个授权, 他人授权 1 vs 5000; 输出: 考察数 %d vs %d; 依据: 只扫描本人授权", c1, c2)
	if c1 != 2 || c2 != 2 {
		t.Fatalf("accessChecks = %d, %d; want 2, 2", c1, c2)
	}
}

func TestHistoryStableAcrossReview(t *testing.T) {
	s := newSvc(t)
	setupExample(t, s)
	if err := s.Request(10, "g1", "alice", "/db/prod", 100); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := s.Register(39, "bob", policy.Engineer, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	times := []int64{10, 25, 39}
	before := make([]bool, len(times))
	for i, at := range times {
		got, err := s.Access("alice", "/db/prod", at)
		if err != nil {
			t.Fatalf("Access before review: %v", err)
		}
		before[i] = got
	}
	if err := s.Approve(40, "g1", "m1"); err != nil { // 评审晚于所有考察时刻
		t.Fatalf("Approve: %v", err)
	}
	for i, at := range times {
		got, err := s.Access("alice", "/db/prod", at)
		if err != nil {
			t.Fatalf("Access after review: %v", err)
		}
		t.Logf("输入: t=%d; 输出: 评审前 %v 评审后 %v; 依据: 历史可重复读", at, before[i], got)
		if got != before[i] {
			t.Fatalf("Access at %d changed: %v -> %v", at, before[i], got)
		}
	}
}

func TestRegisterValidation(t *testing.T) {
	s := newSvc(t)
	longName := string(make([]byte, 65))
	wantErr(t, s.Register(0, "", policy.Engineer, nil), ErrInvalidParam)
	wantErr(t, s.Register(0, longName, policy.Engineer, nil), ErrInvalidParam)
	wantErr(t, s.Register(0, "e1", policy.Engineer, []string{"/a"}), ErrInvalidParam)
	wantErr(t, s.Register(0, "m1", policy.Manager, nil), ErrInvalidParam)
	wantErr(t, s.Register(0, "m1", policy.Manager, []string{"/a/"}), ErrInvalidParam)
	wantErr(t, s.Register(0, "m1", policy.Manager, []string{"a/b"}), ErrInvalidParam)
	wantErr(t, s.Register(0, "m1", policy.Manager, []string{"/a//b"}), ErrInvalidParam)
	wantErr(t, s.Register(0, "x", policy.Role("root"), nil), ErrInvalidParam)
	if err := s.Register(5, "alice", policy.Engineer, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	wantErr(t, s.Register(4, "alice", policy.Engineer, nil), ErrClockRegression) // 时钟先于重复
	wantErr(t, s.Register(6, "alice", policy.Engineer, nil), ErrDuplicate)
	if got := s.Clock(); got != 5 { // 被拒绝的操作不推进时钟
		t.Fatalf("clock = %d, want 5", got)
	}
}
