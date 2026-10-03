package altruistic

import (
	"errors"
	"testing"
)

func newT(t *testing.T, n int) *Manager {
	t.Helper()
	m, err := New(n)
	if err != nil {
		t.Fatalf("New(%d): %v", n, err)
	}
	return m
}

func errKind(err error) string {
	if err == nil {
		return "ok"
	}
	var le *LockError
	if errors.As(err, &le) {
		return le.Kind
	}
	return err.Error()
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func mustKind(t *testing.T, err error, want, ctx string) *LockError {
	t.Helper()
	if got := errKind(err); got != want {
		t.Fatalf("%s: kind = %s, want %s (err=%v)", ctx, got, want, err)
	}
	var le *LockError
	errors.As(err, &le)
	return le
}

func TestInvalidN(t *testing.T) {
	for _, n := range []int{0, -1, 65, 1000} {
		if _, err := New(n); errKind(err) != "invalid_n" {
			t.Fatalf("New(%d): %v, want invalid_n", n, err)
		}
	}
	for _, n := range []int{1, 64} {
		if _, err := New(n); err != nil {
			t.Fatalf("New(%d): %v", n, err)
		}
	}
}

func TestValidationOrder(t *testing.T) {
	m := newT(t, 2)
	t1 := m.Begin()

	mustKind(t, m.Lock(99, 0), "unknown_transaction", "lock unknown txn")
	mustKind(t, m.Donate(99, 0), "unknown_transaction", "donate unknown txn")
	mustKind(t, m.Finish(99), "unknown_transaction", "finish unknown txn")
	if a, err := m.Abort(99); errKind(err) != "unknown_transaction" || a != nil {
		t.Fatalf("abort unknown: a=%v err=%v", a, err)
	}

	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Finish(t1), "t1 finish")
	mustKind(t, m.Lock(t1, 5), "not_active", "finished txn beats object range")
	mustKind(t, m.Donate(t1, 0), "not_active", "donate finished")
	mustKind(t, m.Finish(t1), "not_active", "finish again")
	if _, err := m.Abort(t1); errKind(err) != "not_active" {
		t.Fatalf("abort finished: %v", err)
	}

	t2 := m.Begin()
	mustKind(t, m.Lock(t2, 2), "object_out_of_range", "lock oob")
	mustKind(t, m.Lock(t2, -1), "object_out_of_range", "lock negative")
	mustKind(t, m.Donate(t2, 2), "object_out_of_range", "donate oob")
}

func TestAlreadyDonatedCannotRelock(t *testing.T) {
	m := newT(t, 2)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "lock 0")
	mustOK(t, m.Donate(t1, 0), "donate 0")
	le := mustKind(t, m.Lock(t1, 0), "already_donated", "relock donated")
	if le.Transaction != t1 || le.Object != 0 {
		t.Fatalf("error context = %+v", le)
	}
	if m.holder[0] != -1 {
		t.Fatalf("holder = %d, want -1 (state unchanged)", m.holder[0])
	}
}

func TestHeldCheckedBeforeWake(t *testing.T) {
	m := newT(t, 3)
	a, b, c := m.Begin(), m.Begin(), m.Begin()
	mustOK(t, m.Lock(a, 0), "a lock 0")
	mustOK(t, m.Donate(a, 0), "a donate 0")
	mustOK(t, m.Lock(b, 0), "b lock 0")
	mustOK(t, m.Lock(c, 2), "c lock 2")
	le := mustKind(t, m.Lock(b, 2), "object_held", "held before wake")
	if le.Transaction != c {
		t.Fatalf("holder reported = %d, want %d", le.Transaction, c)
	}
}

func TestWakeUsesLockedHistory(t *testing.T) {
	m := newT(t, 4)
	t1, t2 := m.Begin(), m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 1), "t1 lock 1")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Donate(t1, 1), "t1 donate 1")
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustOK(t, m.Lock(t2, 1), "t2 lock 1")
	le := mustKind(t, m.Lock(t2, 3), "wake_violation", "one extra object fails")
	if le.Transaction != t1 {
		t.Fatalf("violator = %d, want %d", le.Transaction, t1)
	}
	mustKind(t, m.Lock(t2, 3), "wake_violation", "state unchanged after reject")
	mustOK(t, m.Lock(t1, 3), "donor may lock new object")
}

func TestNoRetroactiveWake(t *testing.T) {
	m := newT(t, 4)
	u, tx := m.Begin(), m.Begin()
	mustOK(t, m.Lock(u, 0), "u lock 0 first")
	mustOK(t, m.Donate(u, 0), "u donate 0")
	mustOK(t, m.Lock(tx, 0), "tx lock 0 (in u's wake)")
	mustOK(t, m.Donate(tx, 0), "tx donates 0 afterward")
	mustOK(t, m.Finish(u), "u not retroactively bound by tx")
	mustOK(t, m.Finish(tx), "tx finishes once u finished")
}

func TestSubsetBoundary(t *testing.T) {
	m := newT(t, 4)
	t1, t2, t3 := m.Begin(), m.Begin(), m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 1), "t1 lock 1")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Donate(t1, 1), "t1 donate 1")
	mustOK(t, m.Lock(t2, 0), "t2 {0} passes")
	mustOK(t, m.Lock(t2, 1), "t2 {0,1} == donated passes")
	aborted, err := m.Abort(t2)
	mustOK(t, err, "abort t2 frees 0,1")
	if len(aborted) != 1 || aborted[0] != t2 {
		t.Fatalf("abort t2 = %v, want [%d]", aborted, t2)
	}
	mustOK(t, m.Lock(t3, 3), "t3 {3} held first")
	mustKind(t, m.Lock(t3, 0), "wake_violation", "S'={0,3} has one extra object")
}

// 多个活跃事务同时约束时报事务号最小的违规者：
// 根捐赠者 e1 捐 {0,1,2}，e2 在其尾流中再捐 {0,1}，e3 再捐 {0}；
// u 锁 0 后同时受三者约束，不同的 S' 可精确区分“唯一违规者”与“取最小者”。
func TestSmallestViolator(t *testing.T) {
	m2 := newT(t, 8)
	e1, e2, e3 := m2.Begin(), m2.Begin(), m2.Begin()
	for _, o := range []int{0, 1, 2} {
		mustOK(t, m2.Lock(e1, o), "e1 lock")
	}
	for _, o := range []int{0, 1, 2} {
		mustOK(t, m2.Donate(e1, o), "e1 donate")
	}
	// e2 捐 {0,1}：锁 0、1 时只受 e1 约束（e3 尚未捐任何对象）。
	mustOK(t, m2.Lock(e2, 0), "e2 lock 0")
	mustOK(t, m2.Lock(e2, 1), "e2 lock 1")
	mustOK(t, m2.Donate(e2, 0), "e2 donate 0")
	mustOK(t, m2.Donate(e2, 1), "e2 donate 1")
	// e3 捐 {0}：锁 0 时受 e1、e2 约束，S'={0}⊆{0,1} 合法。
	mustOK(t, m2.Lock(e3, 0), "e3 lock 0")
	mustOK(t, m2.Donate(e3, 0), "e3 donate 0")
	u := m2.Begin()
	mustOK(t, m2.Lock(u, 0), "u lock 0 (wk e1,e2,e3)")
	// S'={0,1}：e1、e2 允许，e3 不允许（1∉{0}）→ 唯一违规者 e3。
	le := mustKind(t, m2.Lock(u, 1), "wake_violation", "e3 is the only violator")
	if le.Transaction != e3 {
		t.Fatalf("violator = %d, want %d", le.Transaction, e3)
	}
	// S'={0,2}：e1 允许，e2（2∉{0,1}）与 e3（2∉{0}）都违规 → 最小 e2。
	le = mustKind(t, m2.Lock(u, 2), "wake_violation", "smallest of e2,e3")
	if le.Transaction != e2 {
		t.Fatalf("violator = %d, want %d", le.Transaction, e2)
	}
}

func TestConstraintDiesWithDonor(t *testing.T) {
	m := newT(t, 4)
	t1, t2 := m.Begin(), m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustKind(t, m.Lock(t2, 2), "wake_violation", "constraint alive")
	mustOK(t, m.Finish(t1), "t1 finish")
	mustOK(t, m.Lock(t2, 2), "constraint gone after donor finish")
	mustOK(t, m.Finish(t2), "t2 finish")
}

func TestFinishWake(t *testing.T) {
	m := newT(t, 6)
	t1, t2, t3, u := m.Begin(), m.Begin(), m.Begin(), m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustOK(t, m.Donate(t2, 0), "t2 donate 0")
	mustOK(t, m.Lock(t3, 0), "t3 lock 0")
	mustKind(t, m.Finish(t3), "wake_unfinished", "t3 blocked")
	mustOK(t, m.Finish(u), "u finishes freely")
	mustOK(t, m.Finish(t1), "t1 finish first")
	le := mustKind(t, m.Finish(t3), "wake_unfinished", "still blocked by t2")
	if le.Transaction != t2 {
		t.Fatalf("unfinished reported = %d, want %d", le.Transaction, t2)
	}
	mustOK(t, m.Finish(t2), "t2 finish")
	mustOK(t, m.Finish(t3), "t3 finish after wake cleared")
}

func TestAbortCascadeThreeLayers(t *testing.T) {
	m := newT(t, 16)
	r1, r2 := m.Begin(), m.Begin()
	mustOK(t, m.Lock(r1, 0), "r1 lock 0")
	mustOK(t, m.Donate(r1, 0), "r1 donate 0")
	mustOK(t, m.Lock(r1, 1), "r1 lock 1")
	mustOK(t, m.Donate(r1, 1), "r1 donate 1")
	mustOK(t, m.Lock(r1, 2), "r1 lock 2")
	mustOK(t, m.Donate(r1, 2), "r1 donate 2")
	mustOK(t, m.Lock(r2, 3), "r2 lock 3")
	mustOK(t, m.Donate(r2, 3), "r2 donate 3")
	a, b, x := m.Begin(), m.Begin(), m.Begin()
	mustOK(t, m.Lock(a, 0), "a lock r1 donation 0")
	mustOK(t, m.Lock(a, 1), "a lock 1 for re-donation")
	mustOK(t, m.Donate(a, 1), "a donate 1")
	mustOK(t, m.Lock(b, 3), "b lock r2 donation 3")
	mustOK(t, m.Lock(x, 4), "x lock independent 4")
	c, y := m.Begin(), m.Begin()
	mustOK(t, m.Lock(c, 1), "c lock a donation 1")
	mustOK(t, m.Lock(y, 5), "y lock independent 5")

	got, err := m.Abort(r1)
	mustOK(t, err, "abort r1")
	want := []int{r1, a, c}
	if len(got) != len(want) {
		t.Fatalf("abort set = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("abort set = %v, want ascending %v", got, want)
		}
	}
	for _, txn := range []int{r2, b, x, y} {
		if m.state[txn-1] != stateActive {
			t.Fatalf("txn %d not active, cascade leaked", txn)
		}
	}
	for _, txn := range want {
		if m.state[txn-1] != stateAborted {
			t.Fatalf("txn %d not aborted", txn)
		}
	}
}

func TestLockAgainAfterAbort(t *testing.T) {
	m := newT(t, 4)
	t1, t2, t3 := m.Begin(), m.Begin(), m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustOK(t, m.Lock(t3, 2), "t3 holds 2")
	got, err := m.Abort(t1)
	mustOK(t, err, "abort t1")
	if len(got) != 2 || got[0] != t1 || got[1] != t2 {
		t.Fatalf("abort set = %v, want [%d %d]", got, t1, t2)
	}
	mustKind(t, m.Lock(m.Begin(), 2), "object_held", "t3 still holds 2")
	t4 := m.Begin()
	mustOK(t, m.Lock(t4, 0), "relock released 0 after abort")
	mustKind(t, m.Lock(t4, 0), "ok", "re-lock own object is idempotent")
}

func TestHoldingInvariant(t *testing.T) {
	m := newT(t, 8)
	txns := make([]int, 5)
	for i := range txns {
		txns[i] = m.Begin()
	}
	mustOK(t, m.Lock(txns[0], 0), "lock 0")
	mustOK(t, m.Donate(txns[0], 0), "donate 0")
	mustOK(t, m.Lock(txns[1], 0), "relock 0 by t2")
	mustOK(t, m.Lock(txns[2], 1), "lock 1")
	m.checkInvariant(t)
	_, err := m.Abort(txns[0])
	mustOK(t, err, "abort")
	m.checkInvariant(t)
}

func (m *Manager) checkInvariant(t *testing.T) {
	t.Helper()
	var seen uint64
	for i, st := range m.state {
		if st != stateActive {
			if m.holding[i] != 0 {
				t.Fatalf("inactive txn %d still holds %b", i+1, m.holding[i])
			}
			continue
		}
		if m.holding[i]&seen != 0 {
			t.Fatalf("holding sets overlap at txn %d: %b", i+1, m.holding[i])
		}
		seen |= m.holding[i]
		h := m.holding[i]
		for h != 0 {
			b := trailingZeros64(h)
			if m.holder[b] != i+1 {
				t.Fatalf("holder[%d] = %d, want %d", b, m.holder[b], i+1)
			}
			h &= h - 1
		}
	}
}
