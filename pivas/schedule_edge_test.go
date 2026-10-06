package pivas

import "testing"

func benchSys(t *testing.T, gap, cap int) *System {
	s := New()
	mustOK(t, s.SetTransport(0, Room, 10))
	mustOK(t, s.SetTransport(0, Cold, 20))
	for n, d := range map[int]int{1: 100, 2: 150, 3: 190} {
		mustOK(t, s.SetDuration(0, n, d))
	}
	mustOK(t, s.RegisterBench(0, Bench{ID: "A", Capacity: cap, ClearGap: gap}))
	mustOK(t, s.RegisterDrug(0, "d", Drug{RoomStableSec: 100000, ColdStableSec: 200000, SolventClass: "NS"}))
	return s
}

func TestClearGapExactAndOneSecondShort(t *testing.T) {
	s := benchSys(t, 10, 1)
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o1", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o2", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	if i, _ := s.QueryOrder(0, "o2"); i.BatchStart != 110 {
		t.Fatalf("gap exact: want 110 got %d", i.BatchStart)
	}

	s9 := benchSys(t, 9, 1)
	mustOK(t, s9.AcceptOrder(0, OrderInput{ID: "o1", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	mustOK(t, s9.AcceptOrder(0, OrderInput{ID: "o2", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	if i, _ := s9.QueryOrder(0, "o2"); i.BatchStart != 109 {
		t.Fatalf("gap 9: want 109 got %d", i.BatchStart)
	}
}

func TestCapacityExactlyFullOpensNewBatch(t *testing.T) {
	s := benchSys(t, 10, 2)
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "a", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "b", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	ia, _ := s.QueryOrder(0, "a")
	ib, _ := s.QueryOrder(0, "b")
	if ia.BatchID != ib.BatchID {
		t.Fatalf("capacity 2: first two share batch")
	}
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "c", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	ic, _ := s.QueryOrder(0, "c")
	if ic.BatchID == ib.BatchID || ic.BatchStart != 160 {
		t.Fatalf("third opens batch at 160: %+v", ic)
	}
}

// TestUrgentJoinSucceedsWithoutShift 取消后紧急补回，end 回到锚点，恰好不位移。
func TestUrgentJoinSucceedsTight(t *testing.T) {
	s := benchSys(t, 50, 3)
	for _, id := range []string{"p1", "x1", "fill"} {
		mustOK(t, s.AcceptOrder(0, OrderInput{ID: id, Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	}
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "p2", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	mustOK(t, s.CancelOrder(0, "fill")) // B1 3->2 张 end 190->150，B2 仍 240
	p2, _ := s.QueryOrder(0, "p2")
	if p2.BatchStart != 240 {
		t.Fatalf("setup p2=240 got %d", p2.BatchStart)
	}
	// 紧急补第 3 张：end 150->190，190+50=240 恰好不移动 B2。
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "u1", Urgent: true, Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	u1, _ := s.QueryOrder(0, "u1")
	p2b, _ := s.QueryOrder(0, "p2")
	if u1.BatchStart != 0 || u1.ReadyAt != 190 || u1.BatchID == p2b.BatchID {
		t.Fatalf("urgent joins B1 ready=190: %+v", u1)
	}
	if p2b.BatchStart != 240 {
		t.Fatalf("B2 stays 240 tight, got %d", p2b.BatchStart)
	}
}

// TestUrgentJoinRejectedByOwnDue 紧急医嘱自身无法在任何安排下按时 -> 拒绝且状态不变。
func TestUrgentJoinRejectedByOwnDue(t *testing.T) {
	s := benchSys(t, 50, 3)
	for _, id := range []string{"p1", "x1", "fill"} {
		mustOK(t, s.AcceptOrder(0, OrderInput{ID: id, Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	}
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "p2", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	mustOK(t, s.CancelOrder(0, "fill"))
	// 紧急 due=50：最早送达=ready190+10=200 > 50，任何安排都失败。
	err := s.AcceptOrder(0, OrderInput{ID: "u1", Urgent: true, Drugs: []string{"d"}, Solvent: "NS", DueAt: 50})
	if errCode(err) != ErrNoFeasiblePlan {
		t.Fatalf("want reject, got %v", err)
	}
	if _, err := s.QueryOrder(0, "u1"); errCode(err) != ErrInvalidParam {
		t.Fatalf("rejected order absent")
	}
	p2, _ := s.QueryOrder(0, "p2")
	if p2.BatchStart != 240 {
		t.Fatalf("state changed: p2=%d", p2.BatchStart)
	}
}

// TestNormalJoinDoesNotMoveLaterBatch 普通加入 B1 后 B2 开始时刻不变。
func TestNormalJoinDoesNotMoveLaterBatch(t *testing.T) {
	s := benchSys(t, 10, 3)
	for _, id := range []string{"p1", "x1", "fill"} {
		mustOK(t, s.AcceptOrder(0, OrderInput{ID: id, Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	}
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "p2", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	mustOK(t, s.CancelOrder(0, "fill")) // B1 end=150，B2=200
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "n1", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	n1, _ := s.QueryOrder(0, "n1")
	p2, _ := s.QueryOrder(0, "p2")
	if n1.ReadyAt != 190 || n1.BatchID == p2.BatchID {
		t.Fatalf("n1 joins B1 ready=190: %+v", n1)
	}
	if p2.BatchStart != 200 {
		t.Fatalf("B2 must remain 200, got %d", p2.BatchStart)
	}
}

func TestCannotJoinStartedBatch(t *testing.T) {
	s := benchSys(t, 50, 3)
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o1", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	// now=100 时 B1 已开始（start=0），新医嘱只能新开批次，且取消 o1 报状态不符。
	if err := s.CancelOrder(100, "o1"); errCode(err) != ErrBadState {
		t.Fatalf("cancel started: %v", err)
	}
}

func TestColdRequiresColdTransport(t *testing.T) {
	s := New()
	mustOK(t, s.SetTransport(0, Room, 10))
	mustOK(t, s.SetDuration(0, 1, 100))
	mustOK(t, s.RegisterBench(0, Bench{ID: "A", Capacity: 3, ClearGap: 50}))
	// 室温稳定极短，只有冷藏可行，但冷藏运送未配置 -> 无可行安排。
	mustOK(t, s.RegisterDrug(0, "d", Drug{RoomStableSec: 5, ColdStableSec: 9000, SolventClass: "NS"}))
	if err := s.AcceptOrder(0, OrderInput{ID: "o", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}); errCode(err) != ErrNoFeasiblePlan {
		t.Fatalf("cold transport not configured: %v", err)
	}
	mustOK(t, s.SetTransport(1, Cold, 20))
	mustOK(t, s.AcceptOrder(1, OrderInput{ID: "o", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000}))
	info, _ := s.QueryOrder(1, "o")
	if info.Storage != Cold || info.DeliverAt != 121 {
		t.Fatalf("must choose cold: %+v", info)
	}
}
