package roster

import (
	"errors"
	"testing"
)

func TestRegisterBatchDupIndex(t *testing.T) {
	r := New()
	// 首批 A,B,C 成功。
	err := r.RegisterBatch("b1", "t1", 1000, []Sn{"A", "B", "C"}, 1)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		sns  []Sn
		want int
	}{
		{"intra pair later index", []Sn{"D", "E", "E"}, 2},
		{"earliest later occurrence wins", []Sn{"X", "Y", "X", "Y"}, 2},
		{"existing dup at 0", []Sn{"A", "F"}, 0},
		{"existing vs intra pick smallest", []Sn{"Z", "Z", "A"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := r.RegisterBatch("b", "t1", 1000, c.sns, 2)
			idx, ok := DupIndex(err)
			if !ok || idx != c.want {
				t.Fatalf("want dup index %d, got %d (%v)", c.want, idx, err)
			}
		})
	}
}

func TestRegisterBatchZeroTrace(t *testing.T) {
	r := New()
	base := r.Probes()
	_ = r.RegisterBatch("b1", "t1", 1000, []Sn{"A", "B"}, 1)

	// 含重复的批次必须整批拒绝、不留名单痕迹。
	err := r.RegisterBatch("b2", "t1", 1000, []Sn{"C", "C", "D"}, 2)
	if !errors.Is(err, ErrDupSn) {
		t.Fatalf("want ErrDupSn, got %v", err)
	}
	for _, sn := range []Sn{"C", "D"} {
		if _, err := r.Snapshot(sn); !errors.Is(err, ErrUnknown) {
			t.Fatalf("%s must remain unknown after rejected batch", sn)
		}
	}
	// 时钟也不推进：now=1 的批次必须仍然被接受。
	if err := r.RegisterBatch("b3", "t1", 1000, []Sn{"E"}, 1); err != nil {
		t.Fatalf("clock must not advance on reject: %v", err)
	}

	// 非法参数零痕迹且不动时钟。
	if err := r.RegisterBatch("b4", "t1", 1000, []Sn{"F", ""}, 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if _, err := r.Snapshot("F"); !errors.Is(err, ErrUnknown) {
		t.Fatal("invalid batch must leave no trace")
	}
	if r.Probes() <= base {
		t.Fatal("probes should increase only on accepted/inspected batches")
	}
}

func TestRegisterBatchProbeBudget(t *testing.T) {
	for _, n := range []int{100, 10_000} {
		r := New()
		sns := make([]Sn, n)
		for i := range sns {
			sns[i] = Sn([]byte{'s', byte(i >> 8), byte(i)})
		}
		before := r.Probes()
		if err := r.RegisterBatch("b", "t", 1000, sns, 1); err != nil {
			t.Fatal(err)
		}
		got := r.Probes() - before
		if got > int64(2*n) {
			t.Fatalf("n=%d probes=%d exceed 2n=%d", n, got, 2*n)
		}
	}
}

func TestClockMonotonic(t *testing.T) {
	r := New()
	if err := r.RegisterBatch("b", "t", 1000, []Sn{"A"}, 10); err != nil {
		t.Fatal(err)
	}
	err := r.RegisterBatch("b2", "t", 1000, []Sn{"B"}, 9)
	if !errors.Is(err, ErrClockBack) {
		t.Fatalf("want ErrClockBack, got %v", err)
	}
	if _, err := r.Snapshot("B"); !errors.Is(err, ErrUnknown) {
		t.Fatal("clockback batch must leave no trace")
	}
}

type fakeHooks struct {
	locked      bool
	conflicts   int
	acquired    int
	released    int
	cleared     int
	acquireFail bool
}

func (f *fakeHooks) Locked(Sn, int64) bool    { return f.locked }
func (f *fakeHooks) NoteConflict(Sn, int64)   { f.conflicts++ }
func (f *fakeHooks) AcquireQuota(Tenant) bool { f.acquired++; return !f.acquireFail }
func (f *fakeHooks) ClearGuard(Sn)            { f.cleared++ }
func (f *fakeHooks) ReleaseQuota(Tenant)      { f.released++ }

func TestGateRejectionOrder(t *testing.T) {
	r := New()
	h := &fakeHooks{}
	if err := r.RegisterBatch("b", "t", 100, []Sn{"A"}, 10); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		sn   Sn
		fp   Fp
		now  int64
		hk   *fakeHooks
		want error
	}{
		{"invalid sn", "", "f", 11, h, ErrInvalid},
		{"unknown", "Z", "f", 11, h, ErrUnknown},
		{"locked", "A", "f", 11, &fakeHooks{locked: true}, ErrLocked},
		{"closed", "A", "f", 100, h, ErrBatchClosed},
		{"quota", "A", "f", 11, &fakeHooks{acquireFail: true}, ErrQuota},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := r.Gate(c.sn, c.fp, c.now, c.hk)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestGateLifecycle(t *testing.T) {
	r := New()
	h := &fakeHooks{}
	_ = r.RegisterBatch("b", "t", 5000, []Sn{"A", "B"}, 10)

	// Registered 首次绑定：id=1, gen=1。
	rec, err := r.Gate("A", "f1", 20, h)
	if err != nil || rec.ID != 1 || rec.Gen != 1 || rec.State != Activated {
		t.Fatalf("bind: %+v %v", rec, err)
	}
	// 同指纹幂等：不调任何 hook。
	rec, err = r.Gate("A", "f1", 21, h)
	if err != nil || rec.ID != 1 || rec.Gen != 1 ||
		h.conflicts != 0 || h.acquired != 1 || h.released != 0 {
		t.Fatalf("replay must be inert: %+v %v h=%+v", rec, err, h)
	}
	// 异指纹冲突：仅记一次 NoteConflict。
	_, err = r.Gate("A", "f2", 22, h)
	if !errors.Is(err, ErrConflict) || h.conflicts != 1 {
		t.Fatalf("conflict: %v conflicts=%d", err, h.conflicts)
	}
	// Reset：清指纹、清 guard、k 由 guard 自行保留。
	rec, err = r.Reset("A", 23, h)
	if err != nil || rec.State != ResetPending || h.cleared != 1 {
		t.Fatalf("reset: %+v %v", rec, err)
	}
	if _, err := r.Reset("A", 24, h); !errors.Is(err, ErrState) {
		t.Fatalf("reset on non-activated: %v", err)
	}
	// ResetPending 绑定新指纹：id 不变，gen=2，不占名额。
	acqBefore := h.acquired
	rec, err = r.Gate("A", "f9", 2000, h)
	if err != nil || rec.ID != 1 || rec.Gen != 2 || rec.State != Activated {
		t.Fatalf("rebind: %+v %v", rec, err)
	}
	if h.acquired != acqBefore {
		t.Fatal("rebind must not acquire quota")
	}
	// Deactivate：回 Registered，释放名额，id/gen 保留。
	rec, err = r.Deactivate("A", 2050, h)
	if err != nil || rec.State != Registered || rec.ID != 1 || rec.Gen != 2 || h.released != 1 {
		t.Fatalf("deactivate: %+v %v", rec, err)
	}
	// 停用后 B 首次激活，id 连续为 2。
	rec, _ = r.Gate("B", "g1", 2100, h)
	if rec.ID != 2 {
		t.Fatalf("next id must be 2, got %d", rec.ID)
	}
}
