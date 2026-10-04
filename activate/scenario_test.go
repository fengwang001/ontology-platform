package activate

import (
	"errors"
	"fmt"
	"testing"

	"ontology/guard"
	"ontology/roster"
)

func newSvc(t *testing.T, m int, lk int64) *Service {
	t.Helper()
	gd, err := guard.New(m, lk)
	if err != nil {
		t.Fatal(err)
	}
	return New(roster.New(), gd)
}

func errName(err error) string {
	if err == nil {
		return "nil"
	}
	for _, e := range []struct {
		err  error
		name string
	}{
		{ErrInvalid, "ErrInvalid"}, {ErrClockBack, "ErrClockBack"},
		{ErrUnknown, "ErrUnknown"}, {ErrLocked, "ErrLocked"},
		{ErrConflict, "ErrConflict"}, {ErrBatchClosed, "ErrBatchClosed"},
		{ErrQuota, "ErrQuota"}, {ErrDupSn, "ErrDupSn"}, {ErrState, "ErrState"},
	} {
		if errors.Is(err, e.err) {
			return e.name
		}
	}
	return err.Error()
}

// TestSpecExample 完整复刻题目示例：M=2, Lk=100, N=1, until=1000, {A,B}。
func TestSpecExample(t *testing.T) {
	s := newSvc(t, 2, 100)
	if err := s.AddTenant("t", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterBatch("b", "t", 1000, []Sn{"A", "B"}, 0); err != nil {
		t.Fatal(err)
	}

	res, err := s.Activate("A", "f1", 10)
	if err != nil || res != (Result{1, 1}) {
		t.Fatalf("first activate: %+v %v", res, err)
	}
	res, err = s.Activate("A", "f1", 20)
	if err != nil || res != (Result{1, 1}) {
		t.Fatalf("replay: %+v %v", res, err)
	}
	if _, err = s.Activate("A", "f2", 30); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict e=1: %v", err)
	}
	if _, err = s.Activate("A", "f3", 40); !errors.Is(err, ErrConflict) {
		t.Fatalf("threshold call still ErrConflict: %v", err)
	}
	if ge := s.gd.Peek("A"); ge.K != 1 || ge.E != 0 || ge.LockUntil != 140 {
		t.Fatalf("lock state: %+v", ge)
	}
	if _, err = s.Activate("B", "g1", 50); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota: %v", err)
	}
	if _, err = s.Activate("A", "f1", 139); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked at 139: %v", err)
	}
	res, err = s.Activate("A", "f1", 140)
	if err != nil || res != (Result{1, 1}) {
		t.Fatalf("unlocked replay at 140: %+v %v", res, err)
	}

	if _, err := s.Activate("A", "fX", 150); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Activate("A", "fY", 160); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if ge := s.gd.Peek("A"); ge.K != 2 || ge.LockUntil != 360 {
		t.Fatalf("second lock: %+v", ge)
	}
	if err := s.Reset("A", 400); err != nil {
		t.Fatal(err)
	}
	if ge := s.gd.Peek("A"); ge.E != 0 || ge.LockUntil != 0 || ge.K != 2 {
		t.Fatalf("reset clears e/lock, keeps k: %+v", ge)
	}
	res, err = s.Activate("A", "f9", 2000)
	if err != nil || res != (Result{1, 2}) {
		t.Fatalf("rebind after close: %+v %v", res, err)
	}
	if err := s.Deactivate("A", 2050); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Activate("B", "g1", 2100); !errors.Is(err, ErrBatchClosed) {
		t.Fatalf("B closed before quota: %v", err)
	}
	if _, err := s.Activate("A", "f9", 2100); !errors.Is(err, ErrBatchClosed) {
		t.Fatalf("A closed after deactivate: %v", err)
	}
}

func TestUntilBoundary(t *testing.T) {
	s := newSvc(t, 2, 100)
	_ = s.AddTenant("t", 2)
	_ = s.RegisterBatch("b", "t", 1000, []Sn{"B", "C"}, 0)
	if _, err := s.Activate("B", "g1", 999); err != nil {
		t.Fatalf("999 passes until: %v", err)
	}
	if _, err := s.Activate("C", "g2", 1000); !errors.Is(err, ErrBatchClosed) {
		t.Fatalf("now==until must be closed: %v", err)
	}
}

func TestLockCapAndLockedReplay(t *testing.T) {
	s := newSvc(t, 1, 100)
	_ = s.AddTenant("t", 1)
	_ = s.RegisterBatch("b", "t", 1_000_000_000, []Sn{"A"}, 0)
	if _, err := s.Activate("A", "f", 0); err != nil {
		t.Fatal(err)
	}
	var last guard.Entry
	var t0 int64
	for i := 0; i < 10; i++ {
		t0 = int64((i + 1) * 10_000)
		_, err := s.Activate("A", Fp(fmt.Sprintf("x%d", i)), t0)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err call %d: %v", i, err)
		}
		last = s.gd.Peek("A")
	}
	if last.K != 10 || last.LockUntil != t0+6400 {
		t.Fatalf("k=10 cap 2^6: %+v", last)
	}
	if _, err := s.Activate("A", "f", t0+1); !errors.Is(err, ErrLocked) {
		t.Fatalf("correct fp locked too: %v", err)
	}
	if _, err := s.Activate("A", "x0", t0+1); !errors.Is(err, ErrLocked) {
		t.Fatalf("wrong fp locked, must not count: %v", err)
	}
	if ge := s.gd.Peek("A"); ge.E != 0 {
		t.Fatalf("locked calls must not count errors: %+v", ge)
	}
	res, err := s.Activate("A", "f", last.LockUntil)
	if err != nil || res != (Result{1, 1}) {
		t.Fatalf("replay exactly at lockUntil: %+v %v", res, err)
	}
}

func TestRejectionOrder(t *testing.T) {
	s := newSvc(t, 2, 100)
	_ = s.AddTenant("t", 1)
	_ = s.RegisterBatch("b", "t", 100, []Sn{"A"}, 10)
	if _, err := s.Activate("A", "f", 11); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Activate("A", "z1", 12); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Activate("A", "z2", 13); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if !s.gd.Locked("A", 14) {
		t.Fatal("setup lock")
	}
	cases := []struct {
		name string
		sn   Sn
		fp   Fp
		now  int64
		want error
	}{
		{"invalid fp", "A", "", 14, ErrInvalid},
		{"clock back beats locked", "A", "f", 9, ErrClockBack},
		{"unknown beats locked", "Z", "f", 14, ErrUnknown},
		{"locked beats conflict", "A", "other", 14, ErrLocked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Activate(c.sn, c.fp, c.now)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	if _, err := s.Activate("A", "other", 113); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict after unlock: %v", err)
	}
	if _, err := s.Activate("A", "f", 114); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestRegisteredNoConflict(t *testing.T) {
	s := newSvc(t, 1, 100)
	_ = s.AddTenant("t", 1)
	_ = s.RegisterBatch("b", "t", 1000, []Sn{"A"}, 0)
	if _, err := s.Activate("A", "whatever", 5); err != nil {
		t.Fatalf("registered never conflicts: %v", err)
	}
}

func TestBatchRejectZeroTrace(t *testing.T) {
	s := newSvc(t, 1, 100)
	_ = s.AddTenant("t", 1)
	_ = s.RegisterBatch("b1", "t", 1000, []Sn{"A"}, 0)
	err := s.RegisterBatch("b2", "t", 1000, []Sn{"C", "C", "D"}, 1)
	if !errors.Is(err, ErrDupSn) {
		t.Fatal(err)
	}
	if idx, ok := roster.DupIndex(err); !ok || idx != 1 {
		t.Fatalf("dup index: %d %v", idx, ok)
	}
	if _, err := s.Activate("D", "d", 2); !errors.Is(err, ErrUnknown) {
		t.Fatalf("rejected batch leaves no trace: %v", err)
	}
}

func TestResetDeactivateStateGuards(t *testing.T) {
	s := newSvc(t, 2, 100)
	_ = s.AddTenant("t", 1)
	_ = s.RegisterBatch("b", "t", 5000, []Sn{"A"}, 0)
	if err := s.Reset("A", 1); !errors.Is(err, ErrState) {
		t.Fatalf("reset registered: %v", err)
	}
	if _, err := s.Activate("A", "f", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset("A", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset("A", 3); !errors.Is(err, ErrState) {
		t.Fatalf("double reset: %v", err)
	}
	if err := s.Deactivate("A", 4); err != nil {
		t.Fatal(err)
	}
	rec, err := s.rs.Snapshot("A")
	if err != nil || rec.State != roster.Registered || rec.ID != 1 || rec.Gen != 1 || rec.Fp != "" {
		t.Fatalf("deactivated record: %+v %v", rec, err)
	}
	if s.used["t"] != 0 {
		t.Fatalf("quota released: used=%d", s.used["t"])
	}
	res, err := s.Activate("A", "g", 5)
	if err != nil || res != (Result{1, 2}) {
		t.Fatalf("reactivate keeps id, gen+1: %+v %v", res, err)
	}
	if err := s.Deactivate("ZZ", 6); !errors.Is(err, ErrUnknown) {
		t.Fatalf("deactivate unknown: %v", err)
	}
}
