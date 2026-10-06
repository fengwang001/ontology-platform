package ontology

import (
	"errors"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func errIs(got, want error) bool { return errors.Is(got, want) }

// TestIntervals 入住日在住、退出日不在住；同房间交接当天归属新住户。
func TestIntervals(t *testing.T) {
	s := NewService(1_000_000, 100)
	mustOK(t, s.CheckIn(10, 1, 1, 1, 10), "checkin r1")
	mustOK(t, s.CheckOut(20, 1, 15), "checkout r1 at 15")
	mustOK(t, s.CheckIn(20, 2, 1, 15, 10), "checkin r1 handoff")
	r1 := s.residents[1]
	if present(r1, 14) != true || present(r1, 15) != false {
		t.Fatalf("half-open occupancy violated")
	}
	if present(s.residents[2], 15) != true {
		t.Fatalf("handoff day belongs to new resident")
	}
	mustOK(t, s.EnterBill(20, 100, 20, 14, 16, SplitPerHead, 0, true), "bill")
	cs := s.billContribCopy(100)
	sum := map[int64]int64{}
	for _, c := range cs {
		sum[c.ResidentID] = c.Amount
	}
	if sum[1] != 10 || sum[2] != 10 {
		t.Fatalf("handoff day attribution wrong: %+v", cs)
	}
}

// TestEmptyDay 无人在住日由之后最早入住者承担，否则房东承担。
func TestEmptyDay(t *testing.T) {
	s := NewService(1_000_000, 100)
	mustOK(t, s.CheckIn(5, 1, 2, 5, 10), "later resident")
	mustOK(t, s.EnterBill(6, 10, 3, 0, 5, SplitPerHead, 0, true), "empty days")
	cs := s.billContribCopy(10)
	var got int64
	for _, c := range cs {
		if c.ResidentID == 1 {
			got = c.Amount
		}
		if c.ResidentID == 0 {
			t.Fatalf("should not fall to landlord")
		}
	}
	if got != 3 {
		t.Fatalf("empty days should fall to earliest future resident, got %d", got)
	}

	s2 := NewService(1_000_000, 100)
	mustOK(t, s2.EnterBill(3, 9, 7, 0, 7, SplitPerHead, 0, true), "no residents")
	ls, err := s2.LandlordShare(9)
	mustOK(t, err, "landlord share")
	if ls != 7 {
		t.Fatalf("all empty days must fall to landlord, got %d", ls)
	}
}

// TestRemainderOrder 余数按入住日早、房间序号小者先得。
func TestRemainderOrder(t *testing.T) {
	s := NewService(1_000_000, 100)
	mustOK(t, s.CheckIn(10, 1, 3, 2, 10), "late small")
	mustOK(t, s.CheckIn(10, 2, 1, 1, 10), "earlier room1")
	mustOK(t, s.CheckIn(10, 3, 2, 1, 10), "same day room2")
	mustOK(t, s.EnterBill(10, 20, 10, 5, 6, SplitPerHead, 0, true), "bill")
	cs := s.billContribCopy(20)
	got := map[int64]int64{}
	for _, c := range cs {
		got[c.ResidentID] = c.Amount
	}
	if got[1] != 3 || got[2] != 4 || got[3] != 3 {
		t.Fatalf("remainder order wrong: %+v", got)
	}
}

// TestNetReversal 净额可反转且守恒。
func TestNetReversal(t *testing.T) {
	s := NewService(1_000_000, 100)
	mustOK(t, s.CheckIn(1, 1, 1, 1, 10), "r1")
	mustOK(t, s.CheckIn(1, 2, 2, 1, 10), "r2")
	mustOK(t, s.EnterBill(2, 10, 100, 1, 2, SplitPerHead, 1, false), "b1")
	if n, _ := s.NetBetween(1, 2); n != 50 {
		t.Fatalf("expected r1 receivable 50, got %d", n)
	}
	mustOK(t, s.EnterBill(3, 11, 100, 1, 2, SplitPerHead, 2, false), "b2")
	if n, _ := s.NetBetween(1, 2); n != 0 {
		t.Fatalf("bills should cancel, got %d", n)
	}
	mustOK(t, s.EnterBill(4, 12, 100, 1, 2, SplitPerHead, 2, false), "b3")
	if n, _ := s.NetBetween(1, 2); n != -50 {
		t.Fatalf("net must be able to reverse, got %d", n)
	}
	if sumDirected(s) != 0 {
		t.Fatal("sum of resident-directed positions must be zero")
	}
}

// TestCheckoutAndSupplement 退出清算边界与退出后补充清算。
func TestCheckoutAndSupplement(t *testing.T) {
	s := NewService(1_000_000, 100)
	mustOK(t, s.CheckIn(1, 1, 1, 1, 10), "r1")
	mustOK(t, s.CheckIn(1, 2, 2, 1, 10), "r2")
	mustOK(t, s.EnterBill(2, 10, 100, 1, 5, SplitPerHead, 1, false), "pre bill [1,5)")
	mustOK(t, s.CheckOut(10, 2, 6), "r2 out")
	set, err := s.Settlements(2)
	mustOK(t, err, "settlements")
	if len(set) != 1 || set[0].Suppl {
		t.Fatalf("expected one original settlement, got %+v", set)
	}
	var line int64
	for _, l := range set[0].Lines {
		if l.OtherID == 1 {
			line = l.Amount
		}
	}
	if line != -48 {
		t.Fatalf("original settlement line wrong: %d", line)
	}
	mustOK(t, s.EnterBill(12, 11, 100, 3, 8, SplitPerHead, 1, false), "late bill [3,8)")
	set, _ = s.Settlements(2)
	if len(set) != 2 || !set[1].Suppl {
		t.Fatalf("expected supplement settlement, got %+v", set)
	}
	if set[1].Lines[0].OtherID != 1 || set[1].Lines[0].Amount != -30 {
		t.Fatalf("supplement delta wrong: %+v", set[1].Lines)
	}
	if !errIs(s.CheckOut(13, 2, 8), ErrState) {
		t.Fatal("double checkout must be ErrState")
	}
}

// TestDisputeAdjudicate 争议剔除、逾期/重复拒绝、裁定重算与补充清算。
func TestDisputeAdjudicate(t *testing.T) {
	s := NewService(1_000_000, 3)
	mustOK(t, s.CheckIn(1, 1, 1, 1, 10), "r1")
	mustOK(t, s.CheckIn(1, 2, 2, 1, 10), "r2")
	mustOK(t, s.EnterBill(2, 10, 100, 1, 2, SplitPerHead, 1, false), "bill [1,2)")
	mustOK(t, s.CheckOut(5, 2, 3), "r2 out")
	mustOK(t, s.RaiseDispute(5, 10), "dispute")
	if n, _ := s.NetBetween(1, 2); n != 0 {
		t.Fatalf("disputed bill removed from net, got %d", n)
	}
	if !errIs(s.RaiseDispute(5, 10), ErrState) {
		t.Fatal("dispute twice must be ErrState")
	}

	s3 := NewService(1_000_000, 3)
	mustOK(t, s3.CheckIn(1, 1, 1, 1, 10), "r1")
	mustOK(t, s3.EnterBill(2, 20, 10, 1, 2, SplitPerHead, 1, false), "b")
	if !errIs(s3.RaiseDispute(6, 20), ErrState) {
		t.Fatal("late dispute must be ErrState")
	}

	mustOK(t, s.Adjudicate(6, 10, 40), "adjudicate lower")
	if !errIs(s.Adjudicate(6, 10, 20), ErrState) {
		t.Fatal("adjudicate twice must be ErrState")
	}
	s4 := NewService(1_000_000, 10)
	mustOK(t, s4.CheckIn(1, 1, 1, 1, 10), "r1")
	mustOK(t, s4.EnterBill(1, 30, 10, 1, 2, SplitPerHead, 1, false), "b")
	mustOK(t, s4.RaiseDispute(2, 30), "dispute")
	if !errIs(s4.Adjudicate(2, 30, 11), ErrAmount) {
		t.Fatal("adjudication above original must be ErrAmount")
	}
	set, _ := s.Settlements(2)
	if len(set) != 2 || !set[1].Suppl {
		t.Fatalf("adjudication must add supplement, got %+v", set)
	}
	if set[1].Lines[0].Amount != 30 {
		t.Fatalf("adjudication supplement delta wrong: %+v", set[1].Lines)
	}
}

// TestChangeRoom 换房当天按新房间面积分摊。
func TestChangeRoom(t *testing.T) {
	s := NewService(1_000_000, 100)
	mustOK(t, s.CheckIn(1, 1, 1, 1, 10), "r1")
	mustOK(t, s.CheckIn(1, 2, 2, 1, 30), "r2")
	mustOK(t, s.ChangeRoom(5, 1, 3, 5, 30), "r1 moves")
	mustOK(t, s.EnterBill(6, 40, 80, 4, 6, SplitByArea, 0, true), "bill")
	cs := s.billContribCopy(40)
	got := map[int64]int64{}
	for _, c := range cs {
		got[c.ResidentID] = c.Amount
	}
	if got[1] != 20 || got[2] != 60 {
		t.Fatalf("change-room day attribution wrong: %+v", got)
	}
}

// TestErrorOrder 固定错误次序。
func TestErrorOrder(t *testing.T) {
	s := NewService(1_000_000, 10)
	mustOK(t, s.CheckIn(5, 1, 1, 1, 10), "r1")
	if !errIs(s.CheckOut(-1, 999, 9), ErrInvalid) {
		t.Fatal("invalid must precede not-found")
	}
	if !errIs(s.CheckOut(4, 999, 4), ErrClockBack) {
		t.Fatal("clock-back must precede not-found")
	}
	if !errIs(s.CheckOut(6, 999, 6), ErrNotFound) {
		t.Fatal("not-found must precede state")
	}
	if !errIs(s.CheckIn(6, 2, 1, 1, 10), ErrOverlap) {
		t.Fatal("overlap expected")
	}
	if !errIs(s.EnterBill(6, 50, 1_000_001, 1, 2, SplitPerHead, 1, false), ErrAmount) {
		t.Fatal("amount bound expected")
	}
}

// TestRejectedLeavesNoTrace 拒绝操作不留痕，时钟不前进。
func TestRejectedLeavesNoTrace(t *testing.T) {
	s := NewService(100, 10)
	mustOK(t, s.CheckIn(5, 1, 1, 1, 10), "r1")
	beforeRes := len(s.residents)
	beforeNet := sumDirected(s)
	calls := []func() error{
		func() error { return s.CheckIn(6, 2, 1, 1, 10) },                            // 重叠
		func() error { return s.CheckIn(4, 2, 2, 2, 10) },                            // 时钟回退
		func() error { return s.EnterBill(6, 1, 999, 1, 2, SplitPerHead, 1, false) }, // 越界
		func() error { return s.CheckOut(6, 7, 6) },                                  // 不存在
	}
	for _, f := range calls {
		if f() == nil {
			t.Fatal("expected rejection")
		}
	}
	if len(s.residents) != beforeRes {
		t.Fatal("rejected op mutated residents")
	}
	if len(s.bills) != 0 {
		t.Fatal("rejected bill must leave no trace")
	}
	if sumDirected(s) != beforeNet || s.lastNow != 5 {
		t.Fatal("rejected op mutated net or clock")
	}
}

// TestConservation 每张账单份额守恒；净额和为零。
func TestConservation(t *testing.T) {
	s := NewService(1_000_000, 100)
	mustOK(t, s.CheckIn(3, 1, 1, 1, 7), "r1")
	mustOK(t, s.CheckIn(3, 2, 2, 2, 13), "r2")
	mustOK(t, s.EnterBill(4, 10, 1000, 0, 10, SplitPerHead, 1, false), "b1")
	mustOK(t, s.EnterBill(5, 11, 333, 2, 9, SplitByArea, 2, false), "b2")
	for _, bid := range []int64{10, 11} {
		var sum int64
		for _, c := range s.billContribCopy(bid) {
			sum += c.Amount
		}
		if want := s.bills[bid].Amount; sum != want {
			t.Fatalf("bill %d conservation %d != %d", bid, sum, want)
		}
	}
	if sumDirected(s) != 0 {
		t.Fatal("net sum nonzero")
	}
}
