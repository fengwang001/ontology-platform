package alert

import "testing"

var limits = [4]int64{0, 60, 30, 10}

func limit(s int) int64 { return limits[s] }

func TestIngestCreateMergeUpgrade(t *testing.T) {
	b := NewBoard()
	// t=0 上报 v=66（sev1）：新建，deadline=60
	e, created, up := b.Ingest(0, "p1", "K", 66, 1, limit)
	if !created || up || e.ID != 1 || e.Sev != 1 || e.Rep != 66 || e.Deadline != 60 || e.State != StateNotify {
		t.Fatalf("create: %+v", e)
	}
	if b.LastResultTouches() != 1 {
		t.Fatalf("touched=%d want 1", b.LastResultTouches())
	}
	// t=20 上报 70（sev2）：升级，deadline=min(60,50)=50，退回待通知
	e2, created, up := b.Ingest(20, "p1", "K", 70, 2, limit)
	if created || !up || e2 != e || e.Sev != 2 || e.Rep != 70 || e.Deadline != 50 || e.State != StateNotify {
		t.Fatalf("upgrade: %+v", e)
	}
	if len(e.Results) != 2 {
		t.Fatalf("results len=%d want 2", len(e.Results))
	}
	// t=30 上报 71（sev2 同档）：只追加，sev/rep/状态/deadline 不变
	e3, created, up := b.Ingest(30, "p1", "K", 71, 2, limit)
	if created || up || e3 != e || e.Sev != 2 || e.Rep != 70 || e.Deadline != 50 {
		t.Fatalf("same-sev merge: %+v", e)
	}
	// 不同项目另立事件
	e4, created, _ := b.Ingest(0, "p1", "Na", 200, 1, limit)
	if !created || e4.ID != 2 {
		t.Fatalf("new code event id=%d want 2", e4.ID)
	}
	// 升级不会延后时限：t=45 sev3 -> min(50,55)=50
	b.Ingest(45, "p1", "K", 75, 3, limit)
	if e.Deadline != 50 || e.Sev != 3 || e.Rep != 75 {
		t.Fatalf("deadline must not move later: %+v", e)
	}
	// 事件号连续
	if b.Get(1) != e || b.Get(3) != nil {
		t.Fatal("event ids must be contiguous from 1")
	}
}

func TestLandingOrderAndBound(t *testing.T) {
	b := NewBoard()
	// 构造多个事件：不同 deadline，且同 deadline 时按事件号
	a, _, _ := b.Ingest(0, "pa", "K", 75, 3, limit)  // deadline=10, id=1
	c2, _, _ := b.Ingest(0, "pb", "K", 75, 3, limit) // deadline=10, id=2
	c3, _, _ := b.Ingest(0, "pc", "K", 70, 2, limit) // deadline=30, id=3
	_ = c2
	_ = c3
	// now=10 恰等：不落地
	if land := b.LandOverdue(10); len(land) != 0 {
		t.Fatalf("deadline==now must not be overdue, got %d", len(land))
	}
	// now=11：只落 deadline=10 的两个，按 (deadline,id)
	land := b.LandOverdue(11)
	if len(land) != 2 || land[0] != a || land[1].ID != 2 {
		t.Fatalf("landing order=%v", land)
	}
	if b.LastLandingPops() != 2 || b.LastLandingLanded() != 2 {
		t.Fatalf("pops=%d landed=%d", b.LastLandingPops(), b.LastLandingLanded())
	}
	if !a.Late {
		t.Fatal("late flag must be sticky")
	}
	// 再次调用：堆顶 c3 deadline=30 >=11，触碰 1 次但取出 0，落地 0
	land = b.LandOverdue(11)
	if len(land) != 0 || b.LastLandingPops() != 0 || b.LastLandingLanded() != 0 {
		t.Fatalf("empty landing pops=%d", b.LastLandingPops())
	}
	// 已落地逾期的事件不再占未闭环位：同患者同项目可新建
	e, created, _ := b.Ingest(12, "pa", "K", 66, 1, limit)
	if !created || e.ID != 4 {
		t.Fatalf("overdue event removed from open map, got id=%d", e.ID)
	}
}

func TestUpgradeFixesHeapInPlace(t *testing.T) {
	b := NewBoard()
	e, _, _ := b.Ingest(0, "p", "K", 66, 1, limit)  // deadline 60
	c2, _, _ := b.Ingest(0, "q", "K", 75, 3, limit) // deadline 10
	// e 升级：deadline 60 -> min(60,10)=10；同 deadline 时 c2(id=2) 排前
	b.Ingest(0, "p", "K", 75, 3, limit)
	land := b.LandOverdue(11)
	if len(land) != 2 || land[0] != e || land[1] != c2 {
		t.Fatalf("heap.Fix must reorder upgraded event, got %v", land)
	}
}

func TestCloseRemovesFromHeap(t *testing.T) {
	b := NewBoard()
	e, _, _ := b.Ingest(0, "p", "K", 75, 3, limit)
	b.Close(e)
	if e.State != StateClosed || b.LandOverdue(1_000_000_000) != nil {
		t.Fatal("closed event must be removed from due heap and stay unchanged")
	}
}

func TestTouchedIndependentOfOpenCount(t *testing.T) {
	for _, n := range []int{100, 10_000} {
		b := NewBoard()
		for i := 0; i < n; i++ {
			patient := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune(i))
			b.Ingest(0, patient, "K", 66, 1, limit) // 全部 deadline=60
		}
		// 定位最后一个患者的事件：只触碰 1
		key := b.open
		var lastPat string
		for k := range key {
			lastPat = k.patient
		}
		b.Ingest(1, lastPat, "K", 67, 1, limit)
		if b.LastResultTouches() != 1 {
			t.Fatalf("n=%d touched=%d", n, b.LastResultTouches())
		}
		// now=60 恰等，无人落地：pops=0
		b.LandOverdue(60)
		if b.LastLandingPops() != 0 {
			t.Fatalf("n=%d pops at equal deadline=%d", n, b.LastLandingPops())
		}
		// now=61 全部落地，pops 必须 == landed，不得与 n 相关地多取
		b.LandOverdue(61)
		if b.LastLandingPops() != n || b.LastLandingLanded() != n {
			t.Fatalf("n=%d pops=%d landed=%d", n, b.LastLandingPops(), b.LastLandingLanded())
		}
	}
}
