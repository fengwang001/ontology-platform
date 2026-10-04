package activate_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/activate"
	"ontology/roster"
)

func bs(s string) []byte { return []byte(s) }

func newSvc(t *testing.T, m int, lk int64) *activate.Service {
	t.Helper()
	s, err := activate.New(m, lk)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func setupExample(t *testing.T) *activate.Service {
	t.Helper()
	s := newSvc(t, 2, 100)
	r := s.Roster()
	if err := r.AddTenant("t", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterBatch("b", "t", 1000, [][]byte{bs("A"), bs("B")}, 0); err != nil {
		t.Fatal(err)
	}
	return s
}

// 题目给出的完整示例逐步对照。
func TestSpecExample(t *testing.T) {
	s := setupExample(t)

	res, err := s.Activate(bs("A"), bs("f1"), 10)
	if err != nil || res.ID != 1 || res.Gen != 1 {
		t.Fatalf("activate A f1: %+v %v", res, err)
	}
	res, err = s.Activate(bs("A"), bs("f1"), 20)
	if err != nil || !res.Replayed || res.ID != 1 || res.Gen != 1 {
		t.Fatalf("idempotent replay: %+v %v", res, err)
	}

	if _, err = s.Activate(bs("A"), bs("f2"), 30); !errors.Is(err, activate.ErrConflict) {
		t.Fatalf("f2: %v", err)
	}
	if snap, _ := s.Guard().Get(bs("A")); snap.E != 1 {
		t.Fatalf("e=%d want 1", snap.E)
	}

	res, err = s.Activate(bs("A"), bs("f3"), 40)
	if !errors.Is(err, activate.ErrConflict) {
		t.Fatalf("f3: %v", err)
	}
	if res.LockUntil != 140 {
		t.Fatalf("lockUntil=%d want 140", res.LockUntil)
	}
	snap, _ := s.Guard().Get(bs("A"))
	if snap.K != 1 || snap.E != 0 || snap.LockUntil != 140 {
		t.Fatalf("guard=%+v", snap)
	}

	if _, err = s.Activate(bs("B"), bs("g1"), 50); !errors.Is(err, activate.ErrQuota) {
		t.Fatalf("B quota: %v", err)
	}

	if _, err = s.Activate(bs("A"), bs("f1"), 139); !errors.Is(err, activate.ErrLocked) {
		t.Fatalf("locked at 139: %v", err)
	}
	// 锁定期拒绝不改状态：时钟不推进，140 的重放成功。
	res, err = s.Activate(bs("A"), bs("f1"), 140)
	if err != nil || !res.Replayed || res.ID != 1 || res.Gen != 1 {
		t.Fatalf("unlock at ==lockUntil replay: %+v %v", res, err)
	}

	// now=150、160 再错两次：k=2, lockUntil=360
	if _, err = s.Activate(bs("A"), bs("fx"), 150); !errors.Is(err, activate.ErrConflict) {
		t.Fatal(err)
	}
	res, err = s.Activate(bs("A"), bs("fy"), 160)
	if !errors.Is(err, activate.ErrConflict) || res.LockUntil != 360 {
		t.Fatalf("second lock: %+v %v", res, err)
	}

	// Reset(A,400) 后在 until 之后重绑仍成功，gen=2，id 沿用。
	if err = s.Reset(bs("A"), 400); err != nil {
		t.Fatal(err)
	}
	res, err = s.Activate(bs("A"), bs("f9"), 2000)
	if err != nil || res.ID != 1 || res.Gen != 2 {
		t.Fatalf("rebind after reset: %+v %v", res, err)
	}

	// Deactivate 释放名额；B 再激活先撞截止，A 重激活也撞截止。
	if err = s.Deactivate(bs("A"), 2050); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Activate(bs("B"), bs("g1"), 2100); !errors.Is(err, activate.ErrBatchClosed) {
		t.Fatalf("B closed: %v", err)
	}
	if _, err = s.Activate(bs("A"), bs("f9"), 2100); !errors.Is(err, activate.ErrBatchClosed) {
		t.Fatalf("A closed: %v", err)
	}
}

func TestUntilEquality(t *testing.T) {
	s := setupExample(t)
	if _, err := s.Activate(bs("A"), bs("f"), 999); err != nil {
		t.Fatalf("999 should pass deadline: %v", err)
	}
	// B 未激活：now==until 即截止。
	if _, err := s.Activate(bs("B"), bs("g"), 1000); !errors.Is(err, activate.ErrBatchClosed) {
		t.Fatalf("==until: %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	s := setupExample(t)
	// 参数非法先于一切。
	if _, err := s.Activate(nil, bs("f"), 0); !errors.Is(err, activate.ErrInvalid) {
		t.Fatalf("invalid sn: %v", err)
	}
	if _, err := s.Activate(bs("A"), nil, 0); !errors.Is(err, activate.ErrInvalid) {
		t.Fatalf("invalid fp: %v", err)
	}
	if _, err := s.Activate(bs("A"), bs("f"), 1e12+1); !errors.Is(err, activate.ErrInvalid) {
		t.Fatalf("invalid now: %v", err)
	}
	// 先接受 now=100，再回退：未登记的 A 也先报 ClockBack。
	if _, err := s.Activate(bs("A"), bs("f"), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Activate(bs("ZZZ"), bs("f"), 50); !errors.Is(err, activate.ErrClockBack) {
		t.Fatalf("clockback before unknown: %v", err)
	}
	if _, err := s.Activate(bs("ZZZ"), bs("f"), 100); !errors.Is(err, activate.ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}

	// 锁定先于冲突（再建一个名额充足的服务）。
	s2 := newSvc(t, 1, 100) // M=1：第一次错误即锁定
	_ = s2.Roster().AddTenant("t", 10)
	_ = s2.Roster().RegisterBatch("b", "t", 1000, [][]byte{bs("X")}, 0)
	if _, err := s2.Activate(bs("X"), bs("ok"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Activate(bs("X"), bs("bad"), 2); !errors.Is(err, activate.ErrConflict) {
		t.Fatalf("first conflict locks: %v", err)
	}
	// 正确指纹在锁定期也报 ErrLocked，而非幂等成功。
	if _, err := s2.Activate(bs("X"), bs("ok"), 3); !errors.Is(err, activate.ErrLocked) {
		t.Fatalf("locked before replay: %v", err)
	}
	// 锁定期错误指纹也不计数：解锁后原指纹仍幂等。
	if r2, err := s2.Activate(bs("X"), bs("ok"), 102); err != nil || !r2.Replayed {
		t.Fatalf("after unlock replay: %+v %v", r2, err)
	}

	// Registered：截止先于名额。
	s3 := newSvc(t, 2, 100)
	_ = s3.Roster().AddTenant("t", 1)
	_ = s3.Roster().RegisterBatch("b", "t", 10, [][]byte{bs("P"), bs("Q")}, 0)
	if _, err := s3.Activate(bs("P"), bs("f"), 1); err != nil {
		t.Fatal(err)
	}
	// Q：名额满且 now==until(10)，先报截止。
	if _, err := s3.Activate(bs("Q"), bs("f"), 10); !errors.Is(err, activate.ErrBatchClosed) {
		t.Fatalf("closed before quota: %v", err)
	}
}

func TestResetAndDeactivateStateRules(t *testing.T) {
	s := setupExample(t)
	// Registered 不能 Reset/Deactivate。
	if err := s.Reset(bs("A"), 1); !errors.Is(err, activate.ErrState) {
		t.Fatalf("reset registered: %v", err)
	}
	if err := s.Deactivate(bs("A"), 1); !errors.Is(err, activate.ErrState) {
		t.Fatalf("deactivate registered: %v", err)
	}
	if _, err := s.Activate(bs("A"), bs("f1"), 10); err != nil {
		t.Fatal(err)
	}
	// 造一次锁定，再 Reset：清 e 与锁定，k 保留。
	if _, err := s.Activate(bs("A"), bs("bad"), 11); !errors.Is(err, activate.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Activate(bs("A"), bs("bad2"), 12); !errors.Is(err, activate.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Activate(bs("A"), bs("f1"), 13); !errors.Is(err, activate.ErrLocked) {
		t.Fatalf("should be locked: %v", err)
	}
	if err := s.Reset(bs("A"), 13); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Guard().Get(bs("A"))
	if snap.E != 0 || snap.LockUntil != 0 || snap.K != 1 {
		t.Fatalf("after reset guard=%+v", snap)
	}
	// ResetPending 不能再 Reset，但可 Deactivate。
	if err := s.Reset(bs("A"), 14); !errors.Is(err, activate.ErrState) {
		t.Fatalf("reset again: %v", err)
	}
	if err := s.Deactivate(bs("A"), 15); err != nil {
		t.Fatalf("deactivate resetpending: %v", err)
	}
	rec := s.Roster().Get(bs("A"))
	if rec.State != roster.Registered || rec.ID != 1 || rec.Gen != 1 || rec.FP != nil {
		t.Fatalf("after deactivate: %+v", rec)
	}
}

func TestReactivationKeepsIDBumpsGen(t *testing.T) {
	s := setupExample(t)
	if r, err := s.Activate(bs("A"), bs("f1"), 10); err != nil || r.ID != 1 || r.Gen != 1 {
		t.Fatalf("first: %+v %v", r, err)
	}
	if err := s.Deactivate(bs("A"), 20); err != nil {
		t.Fatal(err)
	}
	// 截止内再次激活：沿用 id=1，gen=2。
	if r, err := s.Activate(bs("A"), bs("f2"), 30); err != nil || r.ID != 1 || r.Gen != 2 {
		t.Fatalf("reactivate: %+v %v", r, err)
	}
	// B 此刻得到 id=2（连续无洞）。
	if err := s.Roster().AddTenant("t2", 1); err != nil {
		t.Fatal(err)
	}
	_ = s.Roster().RegisterBatch("b2", "t2", 1000, [][]byte{bs("C")}, 40)
	if r, err := s.Activate(bs("C"), bs("c"), 40); err != nil || r.ID != 2 {
		t.Fatalf("next id: %+v %v", r, err)
	}
}

func TestNonConflictRejectionsDontAdvanceClock(t *testing.T) {
	s := setupExample(t)
	// now=5 未登记拒绝，不应推进时钟；随后 now=3 的合法操作仍可。
	if _, err := s.Activate(bs("ZZ"), bs("f"), 5); !errors.Is(err, activate.ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := s.Activate(bs("A"), bs("f"), 3); err != nil {
		t.Fatalf("clock should not have advanced: %v", err)
	}
	// 名额拒绝也不推进时钟。
	if _, err := s.Activate(bs("B"), bs("g"), 4); !errors.Is(err, activate.ErrQuota) {
		t.Fatal(err)
	}
	if _, err := s.Activate(bs("B"), bs("g"), 4); !errors.Is(err, activate.ErrQuota) {
		t.Fatalf("repeat same now should work: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		m  int
		lk int64
		ok bool
	}{
		{1, 1, true}, {10, 1_000_000, true},
		{0, 100, false}, {11, 100, false}, {2, 0, false}, {2, 1_000_001, false},
	}
	for _, c := range cases {
		_, err := activate.New(c.m, c.lk)
		if c.ok != (err == nil) {
			t.Fatalf("New(%d,%d)=%v ok=%v", c.m, c.lk, err, c.ok)
		}
	}
}

// Activate 一次探查的名单记录数 ≤ 2（sn 记录 1 + 名额 1），与名单总量无关。
func TestProbeScaling(t *testing.T) {
	for _, n := range []int{100, 10_000} {
		s := newSvc(t, 2, 100)
		r := s.Roster()
		if err := r.AddTenant("t", n+1); err != nil {
			t.Fatal(err)
		}
		sns := make([][]byte, n)
		for i := range sns {
			sns[i] = bs(fmt.Sprintf("dev-%05d", i))
		}
		if err := r.RegisterBatch("b", "t", 1_000_000, sns, 0); err != nil {
			t.Fatal(err)
		}
		target := sns[n-1]
		if _, err := s.Activate(target, bs("f"), 10); err != nil {
			t.Fatalf("n=%d activate: %v", n, err)
		}
		if p := r.Probes(); p > 2 {
			t.Fatalf("n=%d probes=%d > 2", n, p)
		}
		// 幂等重放只读记录（不判名额），探查 ≤1。
		if _, err := s.Activate(target, bs("f"), 11); err != nil {
			t.Fatal(err)
		}
		if p := r.Probes(); p > 1 {
			t.Fatalf("n=%d replay probes=%d > 1", n, p)
		}
	}
}
