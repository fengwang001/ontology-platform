package riderassess

import (
	"errors"
	"testing"
)

func testCfg() Config {
	return Config{
		PeriodLen:       100,
		PeriodBase:      0,
		Deductions:      map[EventType]int{TypeLateDelivery: 10, TypeComplaint: 20, TypeRejectOrder: 5, TypeFaultCancel: 8},
		Thresholds:      []int{10, 20, 40},
		AppealWindow:    100,
		ClusterSpan:     10,
		MaxDownPerCycle: 1,
		CompPerLevel:    11,
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func errCode(err error) ErrCode {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

// 周期右端点归属：恰在右端点归入下一周期。
func TestPeriodRightBoundary(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r1"))
	must(t, sys.RegisterEvent(1, Event{ID: "e1", Rider: "r1", OccurAt: 99, Type: TypeRejectOrder}))
	must(t, sys.RegisterEvent(2, Event{ID: "e2", Rider: "r1", OccurAt: 100, Type: TypeRejectOrder}))
	r0, err := sys.QueryPeriod(100, "r1", 0)
	must(t, err)
	r1, err := sys.QueryPeriod(100, "r1", 1)
	must(t, err)
	if r0.Score != 5 || r1.Score != 5 {
		t.Fatalf("boundary assignment wrong: p0=%d p1=%d", r0.Score, r1.Score)
	}
	if !r0.Settled || r1.Settled {
		t.Fatalf("settled flags wrong: %+v %+v", r0, r1)
	}
}

// 阈值取等归更差级。
func TestThresholdEqualityWorse(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeLateDelivery}))
	rep, _ := sys.QueryPeriod(100, "r", 0)
	if rep.Grade != 1 {
		t.Fatalf("score=10 want grade 1, got %d", rep.Grade)
	}
}

// 下降上限逐周期限制，上升不受限。
func TestDownClampAndUnlimitedRise(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeComplaint}))
	must(t, sys.RegisterEvent(2, Event{ID: "b", Rider: "r", OccurAt: 1, Type: TypeComplaint}))
	r0, _ := sys.QueryPeriod(100, "r", 0)
	if r0.Grade != 3 || r0.Benefit != 1 {
		t.Fatalf("p0 grade=%d benefit=%d, want 3/1", r0.Grade, r0.Benefit)
	}
	must(t, sys.RegisterEvent(101, Event{ID: "c", Rider: "r", OccurAt: 101, Type: TypeComplaint}))
	must(t, sys.RegisterEvent(102, Event{ID: "d", Rider: "r", OccurAt: 102, Type: TypeComplaint}))
	r1, _ := sys.QueryPeriod(200, "r", 1)
	if r1.Benefit != 2 {
		t.Fatalf("p1 benefit want 2, got %d", r1.Benefit)
	}
	r2, _ := sys.QueryPeriod(300, "r", 2)
	if r2.Grade != 0 || r2.Benefit != 0 {
		t.Fatalf("p2 grade=%d benefit=%d, want 0/0", r2.Grade, r2.Benefit)
	}
}

// 簇的传递延伸：a-b-c 相邻差均在跨度内即同簇，即使 a 与 c 之差超过跨度。
func TestClusterTransitive(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeLateDelivery, RootCause: "X"}))
	must(t, sys.RegisterEvent(2, Event{ID: "b", Rider: "r", OccurAt: 10, Type: TypeLateDelivery, RootCause: "X"}))
	must(t, sys.RegisterEvent(3, Event{ID: "c", Rider: "r", OccurAt: 20, Type: TypeLateDelivery, RootCause: "X"}))
	rep, _ := sys.QueryPeriod(50, "r", 0)
	if rep.Score != 10 {
		t.Fatalf("transitive cluster score want 10, got %d", rep.Score)
	}
}

// 后登记但发生更早的事件改变簇内最早者，扣分归属随之改变。
func TestLateEarlierEventTakesEarliest(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(10, Event{ID: "b", Rider: "r", OccurAt: 20, Type: TypeComplaint, RootCause: "X"}))
	must(t, sys.RegisterEvent(11, Event{ID: "a", Rider: "r", OccurAt: 15, Type: TypeLateDelivery, RootCause: "X"}))
	rep, _ := sys.QueryPeriod(50, "r", 0)
	if rep.Score != 10 {
		t.Fatalf("want 10 after earliest handoff, got %d", rep.Score)
	}
}

// 撤销簇内最早者：整簇连带撤销；连带事件不可单独申诉。
func TestRevokeEarliestRemovesWholeCluster(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeLateDelivery, RootCause: "X"}))
	must(t, sys.RegisterEvent(2, Event{ID: "b", Rider: "r", OccurAt: 5, Type: TypeComplaint, RootCause: "X"}))
	if err := sys.FileAppeal(4, "ap-b", "b"); errCode(err) != ErrSatelliteEvent {
		t.Fatalf("want satellite error, got %v", err)
	}
	must(t, sys.FileAppeal(5, "ap-a", "a"))
	must(t, sys.RuleAppeal(6, "ap-a", true))
	evA, _ := sys.Event("a")
	evB, _ := sys.Event("b")
	if !evA.Revoked || !evB.Revoked {
		t.Fatalf("whole cluster should be revoked: a=%v b=%v", evA.Revoked, evB.Revoked)
	}
}

// 申诉窗口右端点：恰等于右端点不允许，右端点前一刻允许。
func TestAppealWindowRightBoundary(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "e", Rider: "r", OccurAt: 10, Type: TypeLateDelivery}))
	if err := sys.FileAppeal(110, "ap-late", "e"); errCode(err) != ErrAppealWindowExpired {
		t.Fatalf("at exact window end want expired, got %v", err)
	}
	must(t, sys.FileAppeal(109, "ap", "e"))
}

// 驳回不改任何事件/簇/结算状态，但裁决被记录且不可二次裁决。
func TestRejectAppealNoStateChange(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeLateDelivery, RootCause: "X"}))
	must(t, sys.RegisterEvent(2, Event{ID: "b", Rider: "r", OccurAt: 5, Type: TypeComplaint, RootCause: "X"}))
	must(t, sys.FileAppeal(3, "ap", "a"))
	must(t, sys.RuleAppeal(4, "ap", false))
	rep, _ := sys.QueryPeriod(50, "r", 0)
	if rep.Score != 10 {
		t.Fatalf("reject must keep score, got %d", rep.Score)
	}
	a, _ := sys.Appeal("ap")
	if !a.Ruled || a.Upheld {
		t.Fatalf("ruling not recorded correctly: %+v", a)
	}
	if err := sys.RuleAppeal(51, "ap", true); errCode(err) != ErrAppealRuled {
		t.Fatalf("second ruling must be rejected, got %v", err)
	}
	if c := sys.Compensations("r"); len(c) != 0 {
		t.Fatalf("reject yields no compensation, got %+v", c)
	}
}

// 已结算周期申诉成立且本应更好：补偿一笔，冻结等级/权益不改写。
func TestCompensationOnSettledPeriod(t *testing.T) {
	cfg := testCfg()
	cfg.AppealWindow = 300
	sys, _ := New(cfg)
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeLateDelivery}))
	_, _ = sys.QueryPeriod(100, "r", 0)
	must(t, sys.FileAppeal(150, "ap", "a"))
	must(t, sys.RuleAppeal(151, "ap", true))
	comps := sys.Compensations("r")
	if len(comps) != 1 || comps[0].Amount != 11 || comps[0].Levels != 1 {
		t.Fatalf("want 1 comp of 11, got %+v", comps)
	}
	rep, _ := sys.QueryPeriod(200, "r", 0)
	if rep.Score != 10 || rep.Grade != 1 {
		t.Fatalf("frozen period must not change: %+v", rep)
	}
}

// 已结算周期但本应等级相同：不补偿。
func TestNoCompensationWhenDesiredNotBetter(t *testing.T) {
	cfg := testCfg()
	cfg.AppealWindow = 300
	sys, _ := New(cfg)
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "p", Rider: "r", OccurAt: 0, Type: TypeComplaint}))
	must(t, sys.RegisterEvent(2, Event{ID: "q", Rider: "r", OccurAt: 1, Type: TypeComplaint}))
	must(t, sys.RegisterEvent(3, Event{ID: "u", Rider: "r", OccurAt: 2, Type: TypeRejectOrder}))
	_, _ = sys.QueryPeriod(100, "r", 0)
	must(t, sys.FileAppeal(150, "ap", "u"))
	must(t, sys.RuleAppeal(151, "ap", true))
	if c := sys.Compensations("r"); len(c) != 0 {
		t.Fatalf("same grade -> no compensation, got %+v", c)
	}
}

// 下降上限基准被回溯改写：补偿后下一未开始周期以"本应权益"为基准。
func TestRetroactiveRewritesDownBase(t *testing.T) {
	cfg := testCfg()
	cfg.AppealWindow = 500
	sys, _ := New(cfg)
	must(t, sys.RegisterRider(0, "r"))
	// 用同根因簇：最早者计 20 分，连带事件补足到 40 分需再加一个独立 20 分事件。
	// 简化：直接用两个独立事件，撤销其中一个使 p0 由 grade3->grade2，
	// 本应 benefit1；再验证 p2 高扣分时基准改写生效（3->2，而非 3->3）。
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeComplaint}))
	must(t, sys.RegisterEvent(2, Event{ID: "b", Rider: "r", OccurAt: 2, Type: TypeComplaint}))
	r0, _ := sys.QueryPeriod(100, "r", 0) // 40 grade3 benefit1
	if r0.Benefit != 1 {
		t.Fatalf("p0 benefit want 1, got %d", r0.Benefit)
	}
	must(t, sys.RegisterEvent(101, Event{ID: "c", Rider: "r", OccurAt: 101, Type: TypeComplaint}))
	must(t, sys.RegisterEvent(102, Event{ID: "d", Rider: "r", OccurAt: 102, Type: TypeComplaint}))
	r1, _ := sys.QueryPeriod(200, "r", 1) // 40 grade3 benefit2
	if r1.Benefit != 2 {
		t.Fatalf("p1 benefit want 2, got %d", r1.Benefit)
	}
	// p2 尚未开始（206 < p2 右端点 300）时撤销 a：p0 剩 20 grade2，本应 benefit1（同 frozen benefit 巧合），
	// 为区分基准，改为撤销 p0 全部扣分：让 a 成为独立 40 分事件（类型组合 20+...），
	// 这里使用 a(20)+b(20) 独立，先撤 a 后 p0 grade2；补偿后覆盖基准 desiredBenefit=1。
	must(t, sys.FileAppeal(205, "ap", "a"))
	must(t, sys.RuleAppeal(206, "ap", true))
	comps := sys.Compensations("r")
	if len(comps) != 1 || comps[0].DesiredGrade != 2 {
		t.Fatalf("want comp desired grade 2, got %+v", comps)
	}
	must(t, sys.RegisterEvent(207, Event{ID: "e", Rider: "r", OccurAt: 202, Type: TypeComplaint}))
	must(t, sys.RegisterEvent(208, Event{ID: "f", Rider: "r", OccurAt: 203, Type: TypeComplaint}))
	r2, _ := sys.QueryPeriod(300, "r", 2) // 40 grade3
	// 无改写：base=benefit(p1)=2，最多降 1 -> benefit3；改写后 base=desired=1 -> benefit2。
	if r2.Benefit != 2 {
		t.Fatalf("retroactive base should clamp p2 benefit to 2, got %d", r2.Benefit)
	}
}

// 未结算周期撤销：直接按新扣分结算，不产生补偿。
func TestUnsettledRevokeNoComp(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "a", Rider: "r", OccurAt: 0, Type: TypeComplaint}))
	must(t, sys.FileAppeal(2, "ap", "a"))
	must(t, sys.RuleAppeal(3, "ap", true))
	rep, _ := sys.QueryPeriod(100, "r", 0)
	if rep.Score != 0 || rep.Grade != 0 {
		t.Fatalf("unsettled revoke should yield 0 score/grade, got %+v", rep)
	}
	if c := sys.Compensations("r"); len(c) != 0 {
		t.Fatalf("unsettled revoke yields no comp, got %+v", c)
	}
}

// 时钟回退、对象不存在、已撤销/已申诉等拒绝次序与错误码。
func TestRejectionsAndOrder(t *testing.T) {
	sys, _ := New(testCfg())
	must(t, sys.RegisterRider(10, "r"))
	if err := sys.RegisterRider(9, "r2"); errCode(err) != ErrClockRollback {
		t.Fatalf("want clock rollback, got %v", err)
	}
	if _, err := sys.QueryPeriod(11, "ghost", 0); errCode(err) != ErrRiderNotFound {
		t.Fatalf("want rider not found, got %v", err)
	}
	if err := sys.FileAppeal(12, "ap", "ghost-ev"); errCode(err) != ErrEventNotFound {
		t.Fatalf("want event not found, got %v", err)
	}
	must(t, sys.RegisterEvent(13, Event{ID: "e", Rider: "r", OccurAt: 13, Type: TypeLateDelivery}))
	must(t, sys.FileAppeal(14, "ap", "e"))
	if err := sys.FileAppeal(15, "ap2", "e"); errCode(err) != ErrAlreadyAppealed {
		t.Fatalf("want already appealed, got %v", err)
	}
	if err := sys.RuleAppeal(16, "ghost-ap", true); errCode(err) != ErrAppealNotFound {
		t.Fatalf("want appeal not found, got %v", err)
	}
	must(t, sys.RuleAppeal(17, "ap", true))
	if err := sys.FileAppeal(18, "ap3", "e"); errCode(err) != ErrEventRevoked {
		t.Fatalf("want revoked, got %v", err)
	}
}
