package bus

import (
	"errors"
	"testing"
)

// 测试线路：4 站，站0 与站3 为控制站，中间两个非控制站。
func testScheme() Scheme {
	return Scheme{
		Stops: []StopSpec{
			{Name: "C0", Control: true, DwellSec: 10},
			{Name: "S1", DwellSec: 10},
			{Name: "S2", DwellSec: 10},
			{Name: "C3", Control: true, DwellSec: 10},
		},
		Travel:       []int64{60, 60, 60},
		HeadwaySec:   100,
		HoldCapSec:   100,
		ToleranceSec: 200,
		DutyCapSec:   100000,
	}
}

func newSvc(t *testing.T, sc Scheme) *Service {
	t.Helper()
	s, err := NewService(sc)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func reg(t *testing.T, s *Service, id int64, driver string, at int64) {
	t.Helper()
	if err := s.RegisterDriver(driver, at); err != nil {
		t.Fatalf("RegisterDriver: %v", err)
	}
	if err := s.RegisterTrip(id, driver, at+1); err != nil {
		t.Fatalf("RegisterTrip %d: %v", id, err)
	}
}

func errKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	var be *BusError
	if !errors.As(err, &be) {
		t.Fatalf("not a BusError: %v", err)
	}
	return be.Kind
}

func report(t *testing.T, s *Service, id int64, stop int, at int64) EventView {
	t.Helper()
	ev, err := s.ReportArrival(id, stop, at)
	if err != nil {
		t.Fatalf("ReportArrival trip=%d stop=%d t=%d: %v", id, stop, at, err)
	}
	return *ev
}

func mustSpare(t *testing.T, s *Service, id int64, driver string, at int64) {
	t.Helper()
	if err := s.RegisterDriver(driver, at); err != nil {
		t.Fatalf("RegisterDriver: %v", err)
	}
	if err := s.AddSpare(id, driver, at+1); err != nil {
		t.Fatalf("AddSpare: %v", err)
	}
}

// 到达间隔恰等于 H/2 不算串车；少一秒即串车。
func TestGapExactlyHalfHeadwayNotBunched(t *testing.T) {
	s := newSvc(t, testScheme())
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)
	ev := report(t, s, 20, 0, 150) // gap=50 = H/2
	if ev.Intervention != InterventionNone {
		t.Fatalf("expected no intervention, got %v (%s)", ev.Intervention, ev.Reason)
	}
	if _, _, err := s.RequestIntervention(20, 0, 200); errKind(t, err) != ErrNotBunched {
		t.Fatalf("expected ErrNotBunched, got %v", err)
	}
	s2 := newSvc(t, testScheme())
	reg(t, s2, 10, "d1", 0)
	reg(t, s2, 20, "d2", 2)
	report(t, s2, 10, 0, 100)
	ev2 := report(t, s2, 20, 0, 149) // gap=49 < H/2
	if ev2.Intervention != InterventionHold {
		t.Fatalf("expected HOLD for gap 49, got %v (%s)", ev2.Intervention, ev2.Reason)
	}
}

// 需要的扣车时长恰等于上限时接受，离站间隔恰好 H。
func TestHoldExactlyAtCap(t *testing.T) {
	sc := testScheme()
	sc.HoldCapSec = 60
	s := newSvc(t, sc)
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)       // dep 110
	ev := report(t, s, 20, 0, 140) // need dep gap H: dep=210, hold=210-150=60==cap
	if ev.Intervention != InterventionHold || ev.Departure != 210 {
		t.Fatalf("expected HOLD dep 210 at cap, got %d %v (%s)", ev.Departure, ev.Intervention, ev.Reason)
	}
}

// 扣车推后恰等于最晚允许离站时刻：允许（取等不算超）。
func TestHoldDepartureExactlyLatestAllowed(t *testing.T) {
	sc := testScheme()
	sc.HoldCapSec = 1000
	sc.ToleranceSec = 111 // 首站 planned=100；目标 dep210 严格不越限
	s := newSvc(t, sc)
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)       // dep 110
	ev := report(t, s, 20, 0, 149) // gap=49 串车；自然离站159+扣51=210
	if ev.Intervention != InterventionHold || ev.Departure != 210 {
		t.Fatalf("expected HOLD dep 210 not exceeding latest 211, got %d %v (%s)", ev.Departure, ev.Intervention, ev.Reason)
	}
}

// 超最晚离站改判跳站；跳过区间有已登记下车请求 -> 拒绝跳站，只记录。
func TestSkipRejectedByAlighting(t *testing.T) {
	sc := testScheme()
	sc.HoldCapSec = 1000
	sc.ToleranceSec = 5
	s := newSvc(t, sc)
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)
	if err := s.RegisterAlighting(20, 1, 130); err != nil {
		t.Fatalf("alighting: %v", err)
	}
	ev := report(t, s, 20, 0, 140)
	if ev.Intervention != InterventionNone || ev.Departure != 150 {
		t.Fatalf("expected NONE dep150 after skip rejected, got dep=%d %v (%s)", ev.Departure, ev.Intervention, ev.Reason)
	}
	next := report(t, s, 20, 1, 220)
	if !next.Reported || next.Departure != 230 {
		t.Fatalf("stop1 normal report mismatch: %+v", next)
	}
}

// 无下车请求时跳站生效：被跳过站到站即离站，停站归零，行驶时长不变。
func TestSkipSuccessZeroDwell(t *testing.T) {
	sc := testScheme()
	sc.HoldCapSec = 1000
	sc.ToleranceSec = 5
	s := newSvc(t, sc)
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)
	ev := report(t, s, 20, 0, 140)
	if ev.Intervention != InterventionSkip || ev.Departure != 140 {
		t.Fatalf("expected SKIP dep=140, got dep=%d %v (%s)", ev.Departure, ev.Intervention, ev.Reason)
	}
	v1, err := s.GetEvent(20, 1)
	if err != nil || !v1.Reported || v1.Departure != v1.Arrival || v1.Intervention != InterventionSkip {
		t.Fatalf("skipped stop1 expected arr=dep, got %+v err=%v", v1, err)
	}
	v2, _ := s.GetEvent(20, 2)
	if v2.Arrival != v1.Arrival+60 || v2.Departure != v2.Arrival {
		t.Fatalf("skipped stop2 timing wrong: %+v", v2)
	}
	if _, err := s.ReportArrival(20, 1, 300); errKind(t, err) != ErrDuplicateReport {
		t.Fatalf("expected duplicate on skipped stop, got %v", err)
	}
	report(t, s, 20, 3, v2.Departure+60)
}

// 大间隔恰等于 2H 不插车；严格大于 2H 才插入最小号备车。
func TestGapExactlyTwiceHeadwayNoInsert(t *testing.T) {
	s := newSvc(t, testScheme())
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	mustSpare(t, s, 15, "dx", 4)
	report(t, s, 10, 0, 100)
	ev := report(t, s, 20, 0, 300) // gap=200=2H
	if ev.Intervention != InterventionNone {
		t.Fatalf("expected NONE at gap=2H, got %v", ev.Intervention)
	}
	if len(s.Order()) != 2 {
		t.Fatalf("no insertion expected, order=%v", s.Order())
	}
	s2 := newSvc(t, testScheme())
	reg(t, s2, 10, "d1", 0)
	reg(t, s2, 20, "d2", 2)
	mustSpare(t, s2, 15, "dx", 4)
	report(t, s2, 10, 0, 100)
	report(t, s2, 20, 0, 301) // gap=201>2H
	order := s2.Order()
	if len(order) != 3 || order[0] != 10 || order[1] != 15 || order[2] != 20 {
		t.Fatalf("expected order [10 15 20], got %v", order)
	}
	v, err := s2.GetEvent(15, 0)
	if err != nil || v.Intervention != InterventionSpareInsert || v.Arrival != 200 {
		t.Fatalf("spare event wrong: %+v err=%v", v, err)
	}
}

// 备车池为空：大间隔只记录事件，不报错、不改次序。
func TestBigGapEmptyPool(t *testing.T) {
	s := newSvc(t, testScheme())
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)
	ev := report(t, s, 20, 0, 400)
	if ev.Intervention != InterventionNone {
		t.Fatalf("expected NONE, got %v", ev.Intervention)
	}
	if len(s.Order()) != 2 {
		t.Fatalf("order changed: %v", s.Order())
	}
}

// 工时恰到上限接受扣车；超过则拒绝并降级为不干预（不改判跳站），原因可查询。
func TestHoldAtDutyCapAcceptedAndOverRejected(t *testing.T) {
	sc := testScheme()
	sc.HoldCapSec = 1000
	sc.ToleranceSec = 100000
	sc.DutyCapSec = 61 // 本车 149 到站，dep210 时在岗 61 秒恰等于上限
	s := newSvc(t, sc)
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)
	ev := report(t, s, 20, 0, 149) // 自然离站159，扣51，dep210；在岗 100..210=110<=111
	if ev.Intervention != InterventionHold || ev.Departure != 210 {
		t.Fatalf("expected HOLD dep 210 at duty cap, got %d %v (%s)", ev.Departure, ev.Intervention, ev.Reason)
	}
	sc.DutyCapSec = 60 // dep210 时在岗61 > 60 -> 拒绝扣车
	s2 := newSvc(t, sc)
	reg(t, s2, 10, "d1", 0)
	reg(t, s2, 20, "d2", 2)
	report(t, s2, 10, 0, 100)
	ev2 := report(t, s2, 20, 0, 149)
	if ev2.Intervention != InterventionNone || ev2.Departure != 159 {
		t.Fatalf("expected duty rejection NONE dep159, got dep=%d %v (%s)", ev2.Departure, ev2.Intervention, ev2.Reason)
	}
	kind, reason, err := s2.RequestIntervention(20, 0, 200)
	if err != nil || kind != InterventionNone || reason == "" {
		t.Fatalf("expected queryable downgrade reason, kind=%v reason=%q err=%v", kind, reason, err)
	}
}

// 乱序与重复上报被拒绝且不留痕：时钟、下一站指针、查询结果均不变。
func TestOutOfOrderAndDuplicateNoTrace(t *testing.T) {
	s := newSvc(t, testScheme())
	reg(t, s, 10, "d1", 0)
	report(t, s, 10, 0, 100)

	before, _ := s.GetEvent(10, 1)
	if _, err := s.ReportArrival(10, 2, 200); errKind(t, err) != ErrOutOfOrder {
		t.Fatalf("expected out-of-order, got %v", err)
	}
	if _, err := s.ReportArrival(10, 0, 200); errKind(t, err) != ErrDuplicateReport {
		t.Fatalf("expected duplicate, got %v", err)
	}
	after, _ := s.GetEvent(10, 1)
	if after != before {
		t.Fatalf("rejected reports left a trace: before=%+v after=%+v", before, after)
	}
	if s.Clock() != 100 {
		t.Fatalf("clock changed on rejected op: %d", s.Clock())
	}
	// 合法的下一站上报仍可正常进行。
	report(t, s, 10, 1, 200)
}

// 时钟回退：时刻小于最近被接受操作时刻即拒绝。
func TestClockRollback(t *testing.T) {
	s := newSvc(t, testScheme())
	reg(t, s, 10, "d1", 10)
	_, err := s.ReportArrival(10, 0, 5)
	if errKind(t, err) != ErrClockRollback {
		t.Fatalf("expected clock rollback, got %v", err)
	}
	if s.Clock() != 11 {
		t.Fatalf("clock changed: %d", s.Clock())
	}
}

// 错误类别可区分，且只报优先级最靠前的一类。
func TestErrorKindsAndPrecedence(t *testing.T) {
	s := newSvc(t, testScheme())
	reg(t, s, 10, "d1", 0)
	report(t, s, 10, 0, 100)

	if _, err := s.ReportArrival(10, 0, -1); errKind(t, err) != ErrInvalidParam {
		t.Fatalf("invalid param first, got %v", err)
	}
	if _, err := s.ReportArrival(999, 9, 50); errKind(t, err) != ErrClockRollback {
		t.Fatalf("clock rollback precedes entity lookup, got %v", err)
	}
	if _, err := s.ReportArrival(999, 0, 200); errKind(t, err) != ErrTripNotFound {
		t.Fatalf("trip missing precedes stop missing, got %v", err)
	}
	if _, err := s.ReportArrival(10, 9, 200); errKind(t, err) != ErrStopNotFound {
		t.Fatalf("stop missing precedes ordering, got %v", err)
	}
	if _, err := s.ReportArrival(10, 2, 200); errKind(t, err) != ErrOutOfOrder {
		t.Fatalf("out-of-order precedes duplicate, got %v", err)
	}
	if _, err := s.ReportArrival(10, 0, 200); errKind(t, err) != ErrDuplicateReport {
		t.Fatalf("duplicate, got %v", err)
	}
}

// 司机不存在：绑定未登记司机的车次/备车被拒，且不改变车次次序与时钟。
func TestDriverNotFound(t *testing.T) {
	s := newSvc(t, testScheme())
	if err := s.RegisterTrip(10, "ghost", 100); errKind(t, err) != ErrDriverNotFound {
		t.Fatalf("expected driver not found, got %v", err)
	}
	if err := s.AddSpare(10, "ghost", 100); errKind(t, err) != ErrDriverNotFound {
		t.Fatalf("expected driver not found for spare, got %v", err)
	}
	if len(s.Order()) != 0 || s.Clock() != -1 {
		t.Fatalf("state changed: order=%v clock=%d", s.Order(), s.Clock())
	}
	if _, err := s.GetEvent(10, 0); errKind(t, err) != ErrTripNotFound {
		t.Fatalf("trip must not exist, got %v", err)
	}
}

// 扣车使后续各站整体顺延。
func TestHoldPropagatesDownstream(t *testing.T) {
	s := newSvc(t, testScheme())
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)
	ev := report(t, s, 20, 0, 149) // 串车，自然离站159，扣51，dep 210
	if ev.Departure != 210 {
		t.Fatalf("dep: %d", ev.Departure)
	}
	// 非控制站上报按实际时刻；这里用计划顺延时刻 210+60=270 到站。
	v1 := report(t, s, 20, 1, 270)
	if v1.Departure != 280 {
		t.Fatalf("downstream dwell normal, dep=%d", v1.Departure)
	}
}

// 同一车次最多跳站一次：第二次改判跳站时退回只记录。
func TestSkipAtMostOnce(t *testing.T) {
	sc := Scheme{
		Stops: []StopSpec{
			{Name: "C0", Control: true, DwellSec: 10},
			{Name: "S1", DwellSec: 10},
			{Name: "C2", Control: true, DwellSec: 10},
			{Name: "S3", DwellSec: 10},
			{Name: "C4", Control: true, DwellSec: 10},
		},
		Travel:       []int64{60, 60, 60, 60},
		HeadwaySec:   100,
		HoldCapSec:   1000,
		ToleranceSec: 1,
		DutyCapSec:   100000,
	}
	s := newSvc(t, sc)
	reg(t, s, 10, "d1", 0)
	reg(t, s, 20, "d2", 2)
	report(t, s, 10, 0, 100)
	ev0 := report(t, s, 20, 0, 140)
	if ev0.Intervention != InterventionSkip {
		t.Fatalf("first skip expected, got %v (%s)", ev0.Intervention, ev0.Reason)
	}
	// 前车先通过 S1 到达 C2；跳站快车随后到站，再次串车且扣车超时。
	report(t, s, 10, 1, 230)
	report(t, s, 10, 2, 300)
	ev2 := report(t, s, 20, 2, 301)
	if ev2.Intervention != InterventionNone {
		t.Fatalf("second skip must be refused, got %v (%s)", ev2.Intervention, ev2.Reason)
	}
}

// 方案校验：非法参数返回 ErrInvalidParam。
func TestInvalidScheme(t *testing.T) {
	bad := []Scheme{
		{Stops: []StopSpec{{Name: "a"}}, Travel: nil},
		{Stops: []StopSpec{{Name: "a", DwellSec: 1}, {Name: "a", DwellSec: 1}}, Travel: []int64{1}},
		{Stops: []StopSpec{{Name: "a", DwellSec: 0}, {Name: "b", DwellSec: 1}}, Travel: []int64{1}},
		testSchemeWith(func(sc *Scheme) { sc.Travel = []int64{1, 2} }),
		testSchemeWith(func(sc *Scheme) { sc.HeadwaySec = 0 }),
	}
	for i, sc := range bad {
		if _, err := NewService(sc); errKind(t, err) != ErrInvalidParam {
			t.Fatalf("case %d expected invalid param, got %v", i, err)
		}
	}
}

func testSchemeWith(mut func(*Scheme)) Scheme {
	sc := testScheme()
	mut(&sc)
	return sc
}
