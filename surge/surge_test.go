package surge

import (
	"sync"
	"sync/atomic"
	"testing"
)

func testCfg() Config {
	return Config{
		Thresholds:        []float64{1, 2, 4},
		DowngradeConfirms: 2,
		MaxHeldOrders:     2,
		Subsidies:         []int64{0, 10, 20, 30},
		MinEvalInterval:   5,
	}
}

func newSysT(t *testing.T) *System {
	t.Helper()
	s, e := New(testCfg())
	if e != nil {
		t.Fatalf("new: %v", e)
	}
	must(t, s.AddArea("A"))
	must(t, s.AddArea("B"))
	must(t, s.AddRider("r1"))
	must(t, s.AddRider("r2"))
	must(t, s.AddRider("r3"))
	return s
}

func must(t *testing.T, e *Error) {
	t.Helper()
	if e != nil {
		t.Fatalf("unexpected error kind=%d: %v", e.Kind, e)
	}
}

func wantKind(t *testing.T, e *Error, k ErrorKind) {
	t.Helper()
	if e == nil {
		t.Fatalf("want error kind %d, got nil", k)
	}
	if e.Kind != k {
		t.Fatalf("want error kind %d, got %d (%v)", k, e.Kind, e)
	}
}

func areaCount(s *System, id string) (avail, pending int) {
	a := s.ledger.areas[id]
	return a.available, a.pending
}

func corder(t *testing.T, s *System, orderID, areaID string, at int64) *Error {
	t.Helper()
	_, e := s.CreateOrder(orderID, areaID, at)
	return e
}

func TestRatioExactlyAtThreshold(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	lock, e := s.CreateOrder("o1", "A", 2)
	must(t, e)
	if lock != 0 {
		t.Fatalf("lock at create = %d, want 0", lock)
	}
	must(t, s.Evaluate("A", 3))
	tier, _ := s.CurrentTier("A")
	if tier != 1 {
		t.Fatalf("tier = %d, want 1", tier)
	}
}

func TestInfiniteRatio(t *testing.T) {
	s := newSysT(t)
	must(t, corder(t, s, "o1", "A", 1))
	must(t, s.Evaluate("A", 2))
	tier, _ := s.CurrentTier("A")
	if tier != 3 {
		t.Fatalf("tier = %d, want 3", tier)
	}
	evs, _ := s.TierEvents("A")
	if len(evs) != 1 || !evs[0].RatioInf || evs[0].FromTier != 0 || evs[0].ToTier != 3 {
		t.Fatalf("bad event: %+v", evs)
	}
}

func TestZeroZeroRatio(t *testing.T) {
	s := newSysT(t)
	must(t, s.Evaluate("A", 1))
	tier, _ := s.CurrentTier("A")
	if tier != 0 {
		t.Fatalf("tier = %d, want 0", tier)
	}
	if s.ledger.areas["A"].downConfirms != 0 {
		t.Fatalf("confirms not reset")
	}
}

func TestUpgradeJumpsMultipleTiers(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	for i, id := range []string{"o1", "o2", "o3", "o4", "o5"} {
		must(t, corder(t, s, id, "A", int64(2+i)))
	}
	must(t, s.Evaluate("A", 10))
	tier, _ := s.CurrentTier("A")
	if tier != 3 {
		t.Fatalf("tier = %d, want 3", tier)
	}
}

func TestDowngradeOneStepPerConfirmedCycle(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	for _, id := range []string{"o1", "o2", "o3", "o4"} {
		must(t, corder(t, s, id, "A", 2))
	}
	must(t, s.Evaluate("A", 2))
	for i, id := range []string{"o1", "o2", "o3", "o4"} {
		must(t, s.CancelOrder(id, int64(3+i)))
	}
	must(t, s.Evaluate("A", 10))
	must(t, s.Evaluate("A", 15))
	tier, _ := s.CurrentTier("A")
	if tier != 2 {
		t.Fatalf("tier=%d want 2", tier)
	}
	must(t, s.Evaluate("A", 20))
	must(t, s.Evaluate("A", 25))
	tier, _ = s.CurrentTier("A")
	if tier != 1 {
		t.Fatalf("tier=%d want 1", tier)
	}
	must(t, s.Evaluate("A", 30))
	must(t, s.Evaluate("A", 35))
	tier, _ = s.CurrentTier("A")
	if tier != 0 {
		t.Fatalf("tier=%d want 0", tier)
	}
	evs, _ := s.TierEvents("A")
	if len(evs) != 4 || evs[1].ToTier != 2 || evs[3].ToTier != 0 {
		t.Fatalf("events: %+v", evs)
	}
}

func TestDowngradeInterruptedByUpgradeResetsCount(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	must(t, corder(t, s, "o1", "A", 2))
	must(t, corder(t, s, "o2", "A", 3))
	must(t, s.Evaluate("A", 5))
	must(t, s.CancelOrder("o2", 6))
	must(t, s.Evaluate("A", 10))
	if s.ledger.areas["A"].downConfirms != 1 {
		t.Fatalf("confirms=%d want 1", s.ledger.areas["A"].downConfirms)
	}
	must(t, corder(t, s, "o3", "A", 11))
	must(t, s.Evaluate("A", 15))
	if s.ledger.areas["A"].downConfirms != 0 {
		t.Fatalf("confirms not reset on upward move")
	}
	must(t, s.CancelOrder("o1", 16))
	must(t, s.CancelOrder("o3", 17))
	must(t, s.Evaluate("A", 20))
	tier, _ := s.CurrentTier("A")
	if tier != 2 {
		t.Fatalf("tier=%d want 2 (single fresh confirm must not downgrade)", tier)
	}
}

func TestEqualTargetClearsConfirms(t *testing.T) {
	s := newSysT(t)
	s.ledger.areas["A"].currentTier = 2
	s.ledger.areas["A"].downConfirms = 2
	must(t, s.RiderOnline("r1", "A", 1))
	must(t, corder(t, s, "o1", "A", 2))
	must(t, corder(t, s, "o2", "A", 3))
	must(t, s.Evaluate("A", 10))
	if s.ledger.areas["A"].downConfirms != 0 {
		t.Fatalf("confirms=%d want 0", s.ledger.areas["A"].downConfirms)
	}
}

func TestAtCapacityRemovesRiderFromAvailable(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	if avail, _ := areaCount(s, "A"); avail != 1 {
		t.Fatalf("avail=%d want 1", avail)
	}
	must(t, corder(t, s, "o1", "A", 2))
	must(t, corder(t, s, "o2", "A", 3))
	must(t, s.DispatchOrder("o1", "r1", 4))
	if avail, _ := areaCount(s, "A"); avail != 1 {
		t.Fatalf("avail=%d want 1 after first dispatch", avail)
	}
	must(t, s.DispatchOrder("o2", "r1", 5))
	if avail, _ := areaCount(s, "A"); avail != 0 {
		t.Fatalf("avail=%d want 0 at capacity", avail)
	}
	must(t, s.RiderOnline("r2", "A", 6))
	if avail, _ := areaCount(s, "A"); avail != 1 {
		t.Fatalf("avail=%d want 1 with second rider", avail)
	}
	amt, waived, e := s.CompleteOrder("o1", 7)
	must(t, e)
	if waived || amt != 0 {
		t.Fatalf("amt=%d waived=%v", amt, waived)
	}
	if avail, _ := areaCount(s, "A"); avail != 2 {
		t.Fatalf("avail=%d want 2 after completion", avail)
	}
}

func TestLockedTierUnaffectedByLaterChanges(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	lock, e := s.CreateOrder("o1", "A", 2)
	must(t, e)
	if lock != 0 {
		t.Fatalf("lock=%d want 0", lock)
	}
	must(t, corder(t, s, "o2", "A", 3))
	must(t, corder(t, s, "o3", "A", 4))
	must(t, s.Evaluate("A", 5))
	tier, _ := s.CurrentTier("A")
	if tier != 2 {
		t.Fatalf("tier=%d want 2", tier)
	}
	must(t, corder(t, s, "o4", "A", 6))
	must(t, s.DispatchOrder("o1", "r1", 6))
	amt, waived, e := s.CompleteOrder("o1", 7)
	must(t, e)
	if waived || amt != 0 {
		t.Fatalf("o1 subsidy=%d waived=%v, want 0 by locked tier", amt, waived)
	}
	// o4 在评估之后创建，锁定档 2，补贴 20。
	must(t, s.DispatchOrder("o4", "r1", 8))
	amt, waived, e = s.CompleteOrder("o4", 9)
	must(t, e)
	if waived || amt != 20 {
		t.Fatalf("o4 subsidy=%d waived=%v, want 20", amt, waived)
	}
}

func TestSubsidyEligibilityEntryTimeBoundary(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 5))
	// 先把 A 推到档 2（3 单 / 1 运力，比率 3）。
	must(t, corder(t, s, "o4", "A", 8))
	must(t, corder(t, s, "b1", "A", 9))
	must(t, corder(t, s, "b2", "A", 9))
	must(t, s.Evaluate("A", 10))
	if tier, _ := s.CurrentTier("A"); tier != 2 {
		t.Fatalf("setup tier=%d want 2", tier)
	}
	// o4 在评估前创建，锁档 0；o1 在评估后创建锁档 2。
	_, e := s.CreateOrder("o1", "A", 11)
	must(t, e)
	must(t, s.RiderOnline("r2", "A", 12))
	must(t, s.DispatchOrder("o4", "r2", 13))
	amt, waived, e := s.CompleteOrder("o4", 14)
	must(t, e)
	if !waived || amt != 0 {
		t.Fatalf("late rider subsidy=%d waived=%v, want waived 0", amt, waived)
	}
	must(t, s.DispatchOrder("o1", "r1", 15))
	amt, waived, e = s.CompleteOrder("o1", 16)
	must(t, e)
	if waived || amt != 20 {
		t.Fatalf("boundary rider subsidy=%d waived=%v, want 20", amt, waived)
	}
	total, entries, e := s.RiderSubsidy("r1")
	must(t, e)
	if total != 20 || len(entries) != 1 {
		t.Fatalf("r1 total=%d entries=%d", total, len(entries))
	}
	must(t, s.RiderOnline("r3", "B", 17))
	must(t, corder(t, s, "o5", "B", 18))
	must(t, s.RiderMove("r3", "A", 19))
	must(t, s.RiderMove("r3", "B", 20)) // 再次进入 B 时刻 20 晚于创建 18
	must(t, s.DispatchOrder("o5", "r3", 21))
	amt, waived, _ = s.CompleteOrder("o5", 22)
	if !waived || amt != 0 {
		t.Fatalf("re-entry rider subsidy=%d waived=%v, want waived 0", amt, waived)
	}
}

func TestEvalIntervalEqualityAllowed(t *testing.T) {
	s := newSysT(t)
	must(t, s.Evaluate("A", 10))
	wantKind(t, s.Evaluate("A", 14), KindEvaluationTooFrequent)
	must(t, s.Evaluate("A", 15)) // 恰好等于最小间隔，合法
}

func TestClockRollback(t *testing.T) {
	s := newSysT(t)
	must(t, s.Evaluate("A", 10))
	wantKind(t, s.Evaluate("A", 9), KindClockRollback)
	// 被拒操作不推进时钟：15 仍合法。
	must(t, s.Evaluate("A", 15))
}

func TestErrorOrderingAndKinds(t *testing.T) {
	s := newSysT(t)
	// 空 ID 先于时钟判定。
	wantKind(t, s.Evaluate("", 0), KindInvalidParam)
	// 时钟回退先于对象不存在。
	must(t, s.Evaluate("A", 10))
	wantKind(t, s.Evaluate("ZZZ", 9), KindClockRollback)
	// 对象存在性：区域、骑手、订单可区分。
	wantKind(t, s.RiderOnline("rX", "A", 11), KindRiderNotFound)
	wantKind(t, s.RiderOnline("r1", "Z", 11), KindAreaNotFound)
	// 状态错误：重复上线/下线、无需移动。
	must(t, s.RiderOnline("r1", "A", 12))
	wantKind(t, s.RiderOnline("r1", "A", 13), KindRiderAlreadyOnline)
	wantKind(t, s.RiderMove("r1", "A", 14), KindNoNeedToMove)
	must(t, s.RiderOffline("r1", 15))
	wantKind(t, s.RiderOffline("r1", 16), KindRiderAlreadyOffline)
	// 派单资格次序：不在线 -> 不在本区 -> 持单已满。
	must(t, corder(t, s, "o1", "A", 17))
	wantKind(t, s.DispatchOrder("o1", "r1", 18), KindRiderOffline)
	must(t, s.RiderOnline("r1", "B", 19))
	wantKind(t, s.DispatchOrder("o1", "r1", 20), KindRiderWrongArea)
	must(t, s.RiderMove("r1", "A", 21))
	must(t, corder(t, s, "o2", "A", 22))
	must(t, s.DispatchOrder("o1", "r1", 23))
	must(t, s.DispatchOrder("o2", "r1", 24))
	must(t, corder(t, s, "o3", "A", 25))
	wantKind(t, s.DispatchOrder("o3", "r1", 26), KindRiderAtCapacity)
	// 订单状态错误可区分。
	wantKind(t, s.DispatchOrder("o1", "r1", 27), KindOrderAlreadyDispatched)
	_, _, c3 := s.CompleteOrder("o3", 28)
	wantKind(t, c3, KindOrderAlreadyDispatched)
	_, _, cm := s.CompleteOrder("o1", 29)
	must(t, cm)
	_, _, cm2 := s.CompleteOrder("o1", 30)
	wantKind(t, cm2, KindOrderCompleted)
	must(t, s.CancelOrder("o3", 31))
	wantKind(t, s.CancelOrder("o3", 32), KindOrderCancelled)
	wantKind(t, s.CancelOrder("nope", 33), KindOrderNotFound)
}

func TestPendingCountLifecycle(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	must(t, corder(t, s, "o1", "A", 2))
	must(t, corder(t, s, "o2", "A", 3))
	if _, p := areaCount(s, "A"); p != 2 {
		t.Fatalf("pending=%d want 2", p)
	}
	must(t, s.DispatchOrder("o1", "r1", 4))
	if _, p := areaCount(s, "A"); p != 1 {
		t.Fatalf("pending=%d want 1 after dispatch", p)
	}
	must(t, s.CancelOrder("o2", 5))
	if _, p := areaCount(s, "A"); p != 0 {
		t.Fatalf("pending=%d want 0 after cancel", p)
	}
}

func TestMoveCarryingOrderCompletesInOtherArea(t *testing.T) {
	s := newSysT(t)
	must(t, s.RiderOnline("r1", "A", 1))
	must(t, corder(t, s, "o1", "A", 2))
	must(t, s.DispatchOrder("o1", "r1", 3)) // held 1，仍可用
	must(t, s.RiderMove("r1", "B", 4))      // 运力从 A 转到 B
	if av, _ := areaCount(s, "A"); av != 0 {
		t.Fatalf("A avail=%d want 0", av)
	}
	if av, _ := areaCount(s, "B"); av != 1 {
		t.Fatalf("B avail=%d want 1", av)
	}
	_, _, e := s.CompleteOrder("o1", 5)
	must(t, e) // 在 B 恢复运力，而不是 A
	if av, _ := areaCount(s, "B"); av != 1 {
		t.Fatalf("B avail=%d want 1 after complete", av)
	}
	if av, _ := areaCount(s, "A"); av != 0 {
		t.Fatalf("A avail=%d want 0 after complete", av)
	}
}

// 并发派单不突破持单上限。
func TestConcurrentDispatchRespectsCapacity(t *testing.T) {
	cfg := testCfg()
	cfg.MaxHeldOrders = 1
	s, e := New(cfg)
	must(t, e)
	must(t, s.AddArea("A"))
	must(t, s.AddRider("r1"))
	must(t, s.RiderOnline("r1", "A", 0))
	const n = 64
	for i := 0; i < n; i++ {
		oid := "o" + itoa(i)
		must(t, corder(t, s, oid, "A", int64(i+1)))
	}
	var wg sync.WaitGroup
	var winners int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			oid := "o" + itoa(i)
			if err := s.DispatchOrder(oid, "r1", 100); err == nil {
				atomic.AddInt32(&winners, 1)
			}
		}(i)
	}
	wg.Wait()
	if held := s.ledger.riders["r1"].held; held != 1 {
		t.Fatalf("held=%d want 1", held)
	}
	if winners != 1 {
		t.Fatalf("winners=%d want 1", winners)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
