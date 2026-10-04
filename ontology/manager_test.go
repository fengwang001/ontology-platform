package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func mustOK(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: expected success, got %v", what, err)
	}
}

func mustReject(t *testing.T, err error, reason Reason, blocker int, what string) {
	t.Helper()
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("%s: expected reject %v, got success", what, reason)
	}
	if rej.Reason != reason {
		t.Fatalf("%s: expected reason %v, got %v (%v)", what, reason, rej.Reason, rej)
	}
	if blocker != 0 && rej.Blocker != blocker {
		t.Fatalf("%s: expected blocker %d, got %d", what, blocker, rej.Blocker)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, n := range []int{-1, 0, 65, 100} {
		if _, err := NewManager(n); err == nil {
			t.Fatalf("NewManager(%d): expected config rejection", n)
		}
	}
	for _, n := range []int{1, 64} {
		if _, err := NewManager(n); err != nil {
			t.Fatalf("NewManager(%d): unexpected error %v", n, err)
		}
	}
}

func TestGenericRejectOrder(t *testing.T) {
	m, _ := NewManager(4)
	tx := m.Begin()
	mustReject(t, m.Lock(99, 0), ReasonTxNotFound, 0, "lock unknown tx")
	mustReject(t, m.Lock(tx, 4), ReasonObjectOutOfRange, 0, "lock out of range")
	mustReject(t, m.Lock(tx, -1), ReasonObjectOutOfRange, 0, "lock negative object")
	mustReject(t, m.Donate(99, 0), ReasonTxNotFound, 0, "donate unknown tx")
	mustReject(t, m.Donate(tx, 7), ReasonObjectOutOfRange, 0, "donate out of range")
	mustReject(t, m.Finish(99), ReasonTxNotFound, 0, "finish unknown tx")
	mustOK(t, m.Finish(tx), "finish")
	mustReject(t, m.Lock(tx, 0), ReasonTxNotActive, 0, "lock finished tx")
	mustReject(t, m.Donate(tx, 0), ReasonTxNotActive, 0, "donate finished tx")
	mustReject(t, m.Finish(tx), ReasonTxNotActive, 0, "finish finished tx")
	if _, err := m.Abort(tx); err == nil {
		t.Fatalf("abort finished tx: expected reject, got success")
	}
}

// TestSpecExample 复现题目主示例。
func TestSpecExample(t *testing.T) {
	m, _ := NewManager(4)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 1), "t1 lock 1")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")

	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0 (S'={0} subset of {0})")
	mustReject(t, m.Lock(t2, 2), ReasonWakeViolation, t1, "t2 lock 2 (S'={0,2})")
	mustReject(t, m.Lock(t2, 1), ReasonHeldByOther, t1, "t2 lock 1 held by t1, checked before wake")
	mustOK(t, m.Donate(t1, 1), "t1 donate 1")
	mustOK(t, m.Lock(t2, 1), "t2 lock 1 (S'={0,1} subset of {0,1})")
	mustReject(t, m.Finish(t2), ReasonWakePending, t1, "t2 finish blocked by active t1")

	aborted, err := m.Abort(t1)
	mustOK(t, err, "abort t1")
	if !reflect.DeepEqual(aborted, []int{1, 2}) {
		t.Fatalf("abort t1: expected [1 2], got %v", aborted)
	}
}

// TestSpecExampleLateJoiner 复现题目第二例：先锁 3 再锁已捐赠的 0。
func TestSpecExampleLateJoiner(t *testing.T) {
	m, _ := NewManager(4)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 1), "t1 lock 1")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Donate(t1, 1), "t1 donate 1")

	t3 := m.Begin()
	mustOK(t, m.Lock(t3, 3), "t3 lock 3")
	mustReject(t, m.Lock(t3, 0), ReasonWakeViolation, t1, "t3 lock 0 (S'={3,0} not subset of {0,1})")
}

// TestDonatedCannotRelock 已捐赠的对象不可被捐赠者重新加锁。
func TestDonatedCannotRelock(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustReject(t, m.Lock(t1, 0), ReasonAlreadyDonated, 0, "t1 relock donated 0")
}

// TestWakeUsesFullLockedSet 尾流判定使用曾经加锁过的全部对象（含已捐赠
// 出去的），而不只是当前持有：T2 捐出 0 后当前持有为空，但 locked 仍含 0。
func TestWakeUsesFullLockedSet(t *testing.T) {
	m, _ := NewManager(4)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")

	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustOK(t, m.Donate(t2, 0), "t2 donate 0 (held now empty, locked still {0})")
	mustReject(t, m.Lock(t2, 1), ReasonWakeViolation, t1,
		"t2 lock 1: locked={0,1} not subset of donated(t1)={0} despite empty held set")
}

// TestNoRetroactiveWake 先于 T 的捐赠加锁过某对象的事务，
// 不因 T 之后捐出同一对象而把 T 计入自己的尾流。
func TestNoRetroactiveWake(t *testing.T) {
	m, _ := NewManager(3)
	t1 := m.Begin()
	t2 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0 while free (no donation yet)")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Lock(t2, 0), "t2 lock 0 (wake of t2 gains t1)")
	mustOK(t, m.Donate(t2, 0), "t2 donate 0")
	// t1 在 t2 捐赠 0 之前就锁过 0，t2 不应进入 t1 的尾流；
	// 若被错误计入，locked(t1)={0,1} 不是 donated(t2)={0} 的子集，下面会违规。
	mustOK(t, m.Lock(t1, 1), "t1 lock 1 must succeed: t2 not in wake of t1")
}

// TestSubsetBoundary S' 恰为子集时通过，多一个对象即违规。
func TestSubsetBoundary(t *testing.T) {
	m, _ := NewManager(4)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 1), "t1 lock 1")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Donate(t1, 1), "t1 donate 1")

	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustOK(t, m.Lock(t2, 1), "t2 lock 1 (S'={0,1} exactly subset of {0,1})")
	mustReject(t, m.Lock(t2, 2), ReasonWakeViolation, t1, "t2 lock 2 (S'={0,1,2} too large)")
}

// TestMinBlockerReported 多个活跃事务同时约束时报事务号最小者。
func TestMinBlockerReported(t *testing.T) {
	m, _ := NewManager(4)
	t1 := m.Begin()
	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustOK(t, m.Donate(t2, 0), "t2 donate 0")
	mustOK(t, m.Lock(t1, 0), "t1 lock 0 (wake of t1 gains t2)")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")

	t3 := m.Begin()
	mustOK(t, m.Lock(t3, 0), "t3 lock 0 (wake of t3 gains t1 and t2)")
	mustReject(t, m.Lock(t3, 1), ReasonWakeViolation, t1,
		"t3 lock 1 violates both t1 and t2, min id t1 reported")
}

// TestConstraintDisappearsAfterFinish 约束事务结束后其约束消失。
func TestConstraintDisappearsAfterFinish(t *testing.T) {
	m, _ := NewManager(3)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")

	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustReject(t, m.Lock(t2, 1), ReasonWakeViolation, t1, "t2 lock 1 blocked while t1 active")
	mustOK(t, m.Finish(t1), "t1 finish")
	mustOK(t, m.Lock(t2, 1), "t2 lock 1 ok after t1 finished")
	mustOK(t, m.Finish(t2), "t2 finish ok after t1 finished")
}

// TestFinishBlockedUntilWakeEnds Finish 在尾流中拒绝，尾流事务结束后成功。
func TestFinishBlockedUntilWakeEnds(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")

	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustReject(t, m.Finish(t2), ReasonWakePending, t1, "t2 finish blocked by active t1")
	mustOK(t, m.Finish(t1), "t1 finish")
	mustOK(t, m.Finish(t2), "t2 finish after t1 finished")
}

// TestCascadeAbortThreeLevels 级联中止的传递闭包（三层）与升序返回，
// 且不波及未接触其捐赠对象的事务。
func TestCascadeAbortThreeLevels(t *testing.T) {
	m, _ := NewManager(5)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 1), "t1 lock 1")
	mustOK(t, m.Lock(t1, 2), "t1 lock 2")
	mustOK(t, m.Donate(t1, 0), "t1 donate 0")
	mustOK(t, m.Donate(t1, 1), "t1 donate 1")
	mustOK(t, m.Donate(t1, 2), "t1 donate 2")

	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0 (t2 in cascade level 1)")
	mustOK(t, m.Donate(t2, 0), "t2 donate 0")

	t3 := m.Begin()
	mustOK(t, m.Lock(t3, 0), "t3 lock 0 (t3 in cascade level 2)")
	mustOK(t, m.Donate(t3, 0), "t3 donate 0")

	t4 := m.Begin()
	mustOK(t, m.Lock(t4, 0), "t4 lock 0 (t4 in cascade level 3)")

	t5 := m.Begin()
	mustOK(t, m.Lock(t5, 3), "t5 lock 3 (untouched by donations, must survive)")

	aborted, err := m.Abort(t1)
	mustOK(t, err, "abort t1")
	if !reflect.DeepEqual(aborted, []int{1, 2, 3, 4}) {
		t.Fatalf("abort t1: expected [1 2 3 4], got %v", aborted)
	}
	mustOK(t, m.Lock(t5, 4), "t5 still active and can lock")
	mustOK(t, m.Finish(t5), "t5 finish")
}

// TestAbortReleasesObjects 中止后对象可被重新加锁。
func TestAbortReleasesObjects(t *testing.T) {
	m, _ := NewManager(3)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 1), "t1 lock 1")
	aborted, err := m.Abort(t1)
	mustOK(t, err, "abort t1")
	if !reflect.DeepEqual(aborted, []int{1}) {
		t.Fatalf("abort t1: expected [1], got %v", aborted)
	}
	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0 after t1 aborted")
	mustOK(t, m.Lock(t2, 1), "t2 lock 1 after t1 aborted")
}

// TestFinishReleasesObjects 成功 Finish 释放全部持有。
func TestFinishReleasesObjects(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Finish(t1), "t1 finish")
	t2 := m.Begin()
	mustOK(t, m.Lock(t2, 0), "t2 lock 0 released by t1 finish")
}

// TestLockSelfHeldNoChange 重复加锁自己持有的对象成功且无变化。
func TestLockSelfHeldNoChange(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin()
	mustOK(t, m.Lock(t1, 0), "t1 lock 0")
	mustOK(t, m.Lock(t1, 0), "t1 relock own 0 is a no-op success")
	t2 := m.Begin()
	mustReject(t, m.Lock(t2, 0), ReasonHeldByOther, t1, "t2 lock 0 still held by t1")
}

// TestDonateNotHolder 捐赠非自己持有的对象被拒绝。
func TestDonateNotHolder(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin()
	t2 := m.Begin()
	mustReject(t, m.Donate(t1, 0), ReasonNotHolder, 0, "t1 donate free object")
	mustOK(t, m.Lock(t2, 0), "t2 lock 0")
	mustReject(t, m.Donate(t1, 0), ReasonNotHolder, 0, "t1 donate object held by t2")
}
