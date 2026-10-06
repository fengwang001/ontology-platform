package medsched

import "testing"

// 窗口边界：t-W 恰等允许、t-W-1 无点；now==t+W 仍待给，t+W+1 漏给。
func TestWindowAndMissedBoundary(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 5)
	id := openInterval(t, s, 0, "p1", "A", 100, 100)

	if err := s.Administer(89, id); codeOf(err) != ErrNoScheduledPoint {
		t.Fatalf("t-W-1 应无对应点, got %v", err)
	}
	if err := s.Administer(90, id); err != nil {
		t.Fatalf("t-W 应可给药, got %v", err)
	}

	pts, _ := s.Query(210, "p1", 200, 200)
	if st := statusAt(pts, 200); st != StatusPending {
		t.Fatalf("now=t+W 应待给, got %s", st)
	}
	pts, _ = s.Query(211, "p1", 200, 200)
	if st := statusAt(pts, 200); st != StatusMissed {
		t.Fatalf("now=t+W+1 应漏给, got %s", st)
	}
}

// 安全间隔恰等允许，差一秒报间隔不足（跨医嘱同药品）。
func TestSafetyIntervalAcrossOrders(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 100)
	id1 := openInterval(t, s, 0, "p1", "A", 0, 200)
	id2 := openInterval(t, s, 0, "p1", "A", 100, 200)
	if err := s.Administer(0, id1); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.Administer(99, id2); codeOf(err) != ErrIntervalTooShort {
		t.Fatalf("间隔 99 应不足, got %v", err)
	}
	if err := s.Administer(100, id2); err != nil {
		t.Fatalf("间隔恰等 100 应允许, got %v", err)
	}
}

// 拒服不算实际给药、不参与间隔判定。
func TestRefuseDoesNotAffectInterval(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 100)
	id1 := openInterval(t, s, 0, "p1", "A", 0, 200)
	id2 := openInterval(t, s, 0, "p1", "A", 50, 200)
	if err := s.Refuse(0, id1); err != nil {
		t.Fatalf("refuse: %v", err)
	}
	if err := s.Administer(50, id2); err != nil { // 与拒服相差50<100，仍允许
		t.Fatalf("拒服不参与间隔, got %v", err)
	}
	pts, _ := s.Query(50, "p1", 0, 50)
	if st := statusAt(pts, 0); st != StatusRefused {
		t.Fatalf("应拒服, got %s", st)
	}
}

// 补给两限制恰等：now>t+W、now<next-W；固定间隔补给重排。
func TestMakeUpBoundariesAndReroute(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 5)
	id := openInterval(t, s, 0, "p1", "A", 100, 100)

	if err := s.MakeUp(110, id, 100); codeOf(err) != ErrMakeupNotAllowed {
		t.Fatalf("t+W 不可补给, got %v", err)
	}
	if err := s.MakeUp(190, id, 100); codeOf(err) != ErrMakeupNotAllowed {
		t.Fatalf("next-W 不可补给, got %v", err)
	}
	if err := s.MakeUp(189, id, 100); err != nil {
		t.Fatalf("next-W-1 应可补给, got %v", err)
	}
	pts, _ := s.Query(189, "p1", 100, 100)
	if st := statusAt(pts, 100); st != StatusMadeUp {
		t.Fatalf("应已补给, got %s", st)
	}
	// 新一代 189+100=289；旧点 200 作废。
	pts, _ = s.Query(289, "p1", 200, 289)
	if st := statusAt(pts, 200); st != StatusVoid {
		t.Fatalf("旧点 200 应作废, got %s", st)
	}
	if st := statusAt(pts, 289); st != StatusPending {
		t.Fatalf("新代首点 289 应待给, got %s", st)
	}
}

// 固定时点补给后不重排。
func TestDailyMakeUpNoReroute(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 5)
	id, err := s.OpenOrder(OpenOrderInput{Now: 0, Patient: "p1", Drug: "A",
		Frequency: Frequency{Kind: FreqDaily, Times: []int64{0, 86299}}})
	if err != nil {
		t.Fatalf("open daily: %v", err)
	}
	if err := s.MakeUp(11, id, 0); err != nil {
		t.Fatalf("daily makeup: %v", err)
	}
	pts, _ := s.Query(12, "p1", 0, 86400)
	if st := statusAt(pts, 0); st != StatusMadeUp {
		t.Fatalf("0 点应已补给, got %s", st)
	}
	if st := statusAt(pts, 86299); st != StatusPending {
		t.Fatalf("后续时点不应重排, got %s", st)
	}
}
