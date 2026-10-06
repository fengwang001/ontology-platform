package medsched

import "testing"

// 停嘱分界：planned+W < now 保持漏给，否则作废；停后拒绝给药。
func TestStopBoundary(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 5)
	id := openInterval(t, s, 0, "p1", "A", 100, 100)
	if err := s.StopOrder(111, id); err != nil {
		t.Fatalf("stop: %v", err)
	}
	pts, _ := s.Query(111, "p1", 100, 200)
	if st := statusAt(pts, 100); st != StatusMissed {
		t.Fatalf("点100 应漏给, got %s", st)
	}
	if st := statusAt(pts, 200); st != StatusVoid {
		t.Fatalf("点200 应作废, got %s", st)
	}
	if err := s.Administer(111, id); codeOf(err) != ErrInvalidState {
		t.Fatalf("停嘱后不可给药, got %v", err)
	}
}

// 改嘱原子性：新嘱非法或过敏时旧嘱保持在途。
func TestReviseAtomicity(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 5)
	regDrug(t, s, "B", "c", 5)
	id := openInterval(t, s, 0, "p1", "A", 100, 100)

	if _, err := s.ReviseOrder(50, id, "p1", "A", intervalFreq(50, 15)); codeOf(err) != ErrInvalidParam {
		t.Fatalf("应参数非法, got %v", err)
	}
	if s.orders[id].stoppedAt != 0 {
		t.Fatal("失败后旧嘱不应被停")
	}
	if err := s.AddAllergyDrug(60, "p1", "B"); err != nil {
		t.Fatalf("allergy: %v", err)
	}
	if _, err := s.ReviseOrder(60, id, "p1", "B", intervalFreq(60, 100)); codeOf(err) != ErrAllergy {
		t.Fatalf("应过敏冲突, got %v", err)
	}
	if s.orders[id].stoppedAt != 0 {
		t.Fatal("过敏失败后旧嘱不应被停")
	}
	id2, err := s.ReviseOrder(70, id, "p1", "A", intervalFreq(70, 100))
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if s.orders[id].stoppedAt != 70 || s.orders[id2].openedAt != 70 {
		t.Fatal("改嘱状态错误")
	}
}

// 必要时：同医嘱最小间隔取等、滚动24h次数取等与滑出、跨药品安全间隔。
func TestPRNLimits(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 30)
	id, err := s.OpenOrder(OpenOrderInput{Now: 0, Patient: "p1", Drug: "A",
		Frequency: Frequency{Kind: FreqPRN, PRNMin: 100, PRNLimit: 2}})
	if err != nil {
		t.Fatalf("open prn: %v", err)
	}
	if err := s.AdministerPRN(0, id); err != nil {
		t.Fatalf("prn 0: %v", err)
	}
	if err := s.AdministerPRN(99, id); codeOf(err) != ErrIntervalTooShort {
		t.Fatalf("同嘱间隔不足, got %v", err)
	}
	if err := s.AdministerPRN(100, id); err != nil {
		t.Fatalf("prn 100: %v", err)
	}
	if err := s.AdministerPRN(200, id); codeOf(err) != ErrLimitExceeded {
		t.Fatalf("应次数超限, got %v", err)
	}
	// 在 86401，窗口 (1,86401] 只剩 100 一次。
	if err := s.AdministerPRN(86401, id); err != nil {
		t.Fatalf("滑出窗口应可给, got %v", err)
	}
}

// 过敏命中药品与类别；过敏变更不影响已开医嘱。
func TestAllergyDrugAndCategory(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c1", 5)
	regDrug(t, s, "B", "c2", 5)
	if err := s.AddAllergyDrug(0, "p1", "A"); err != nil {
		t.Fatalf("add drug allergy: %v", err)
	}
	if err := s.AddAllergyCategory(0, "p2", "c2"); err != nil {
		t.Fatalf("add cat allergy: %v", err)
	}
	if _, err := s.OpenOrder(OpenOrderInput{Now: 0, Patient: "p1", Drug: "A", Frequency: intervalFreq(0, 100)}); codeOf(err) != ErrAllergy {
		t.Fatalf("药品过敏应拒绝, got %v", err)
	}
	if _, err := s.OpenOrder(OpenOrderInput{Now: 0, Patient: "p2", Drug: "B", Frequency: intervalFreq(0, 100)}); codeOf(err) != ErrAllergy {
		t.Fatalf("类别过敏应拒绝, got %v", err)
	}
	// p3 先开立，之后再登记过敏：已开医嘱不受影响，仍可给药。
	id := openInterval(t, s, 0, "p3", "A", 100, 100)
	if err := s.AddAllergyDrug(1, "p3", "A"); err != nil {
		t.Fatalf("later allergy: %v", err)
	}
	if err := s.Administer(100, id); err != nil {
		t.Fatalf("已开医嘱不受后续过敏影响, got %v", err)
	}
}

// 时钟回退：被拒操作不改变时钟与状态；错误优先级参数>时钟。
func TestClockRollbackAndPriority(t *testing.T) {
	s := mustSys(t, 10)
	regDrug(t, s, "A", "c", 5)
	id := openInterval(t, s, 100, "p1", "A", 100, 100)
	if err := s.Administer(100, id); err != nil {
		t.Fatalf("give: %v", err)
	}
	if err := s.Administer(99, id); codeOf(err) != ErrClockRollback {
		t.Fatalf("应时钟回退, got %v", err)
	}
	if s.Now() != 100 {
		t.Fatalf("被拒操作不得推进时钟, got %d", s.Now())
	}
	// 参数非法（负 now）优先于时钟回退。
	if err := s.Administer(-1, id); codeOf(err) != ErrInvalidParam {
		t.Fatalf("应参数非法, got %v", err)
	}
	// 不存在对象：时钟先于对象。
	s2 := mustSys(t, 10)
	if _, err := s2.Query(0, "p", 0, 10); err != nil {
		t.Fatalf("空查询应成功: %v", err)
	}
	if err := s2.Administer(-1, "X"); codeOf(err) != ErrInvalidParam {
		t.Fatalf("负now优先, got %v", err)
	}
}
