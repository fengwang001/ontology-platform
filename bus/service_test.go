package bus

import (
	"errors"
	"testing"
)

// 标准方案：5 站（0..4），控制站 0/2/4，h=100，停站 20，行驶 50。
func testConfig(holdCap, tolerance, maxDuty int64) Config {
	return Config{
		StationCount:       5,
		ControlStations:    []int{0, 2, 4},
		TargetHeadway:      100,
		Dwells:             []int64{20, 20, 20, 20, 20},
		TravelTimes:        []int64{50, 50, 50, 50},
		HoldCap:            holdCap,
		DepartureTolerance: tolerance,
		MaxDutySeconds:     maxDuty,
	}
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 未期望的错误 %v", ctx, err)
	}
}

func wantErr(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: 期望 %v，得到 %v", ctx, want, got)
	}
}

// 构造两辆车都已到达控制站 2 的服务。车次1 站2 到站 270。
// 上报顺序严格按时刻交织；车次2 站1 到站由调用方期望的站2 时刻反推。
func twoTripsToControl2(t *testing.T, cfg Config, t2ArriveAt2 int64) *Service {
	return twoTripsToControl2WithSt1(t, cfg, t2ArriveAt2, t2ArriveAt2-70)
}

// twoTripsToControl2WithSt1 允许显式指定车次2在站1的到站时刻。
func twoTripsToControl2WithSt1(t *testing.T, cfg Config, t2At2, t2At1 int64) *Service {
	t.Helper()
	s, err := New(cfg)
	mustOK(t, err, "New")
	mustOK(t, s.RegisterDriver(1), "driver1")
	mustOK(t, s.RegisterDriver(2), "driver2")
	mustOK(t, s.ScheduleTrip(1, 1, 0), "schedule1")
	mustOK(t, s.ScheduleTrip(2, 2, 10), "schedule2")
	mustOK(t, s.ReportArrival(1, 1, 220), "t1 st1")
	mustOK(t, s.ReportArrival(2, 1, t2At1), "t2 st1")
	mustOK(t, s.ReportArrival(1, 2, 270), "t1 st2")
	mustOK(t, s.ReportArrival(2, 2, t2At2), "t2 st2")
	return s
}

// 到达间隔恰等于目标间隔一半（50）不算串车。
func TestBunchHalfEqualIsNotBunched(t *testing.T) {
	s := buildTwoAtControl2(t, testConfig(60, 40, 100000), 270, 320)
	info, err := s.Query(2, 2)
	mustOK(t, err, "query")
	if info.Verdict != VerdictNormal {
		t.Fatalf("期望正常判定，得到 %q", info.Verdict)
	}
	_, err = s.Intervene(2, 2, ActionHold)
	wantErr(t, err, ErrNotBunched, "半间隔干预")
}

// 间隔 49 严格小于一半判串车。
func TestBunchStrictlyBelowHalf(t *testing.T) {
	s := buildTwoAtControl2(t, testConfig(60, 40, 100000), 270, 319)
	info, err := s.Query(2, 2)
	mustOK(t, err, "query")
	if info.Verdict != VerdictBunch {
		t.Fatalf("期望串车判定，得到 %q", info.Verdict)
	}
}

// buildTwoAtControl2 手工交织：车次1 站2 到站 t1At2，车次2 站2 到站 t2At2。
func buildTwoAtControl2(t *testing.T, cfg Config, t1At2, t2At2 int64) *Service {
	t.Helper()
	s, err := New(cfg)
	mustOK(t, err, "New")
	mustOK(t, s.RegisterDriver(1), "d1")
	mustOK(t, s.RegisterDriver(2), "d2")
	mustOK(t, s.ScheduleTrip(1, 1, 0), "s1")
	mustOK(t, s.ScheduleTrip(2, 2, 10), "s2")
	mustOK(t, s.ReportArrival(1, 1, t1At2-50), "a1")
	mustOK(t, s.ReportArrival(1, 2, t1At2), "a2")
	mustOK(t, s.ReportArrival(2, 1, t1At2+10), "b1")
	mustOK(t, s.ReportArrival(2, 2, t2At2), "b2")
	return s
}

// 扣车恰好到上限：需求等于上限，reason=hold-capped。
func TestHoldExactlyCap(t *testing.T) {
	// 前车站2 离站 290；后车站2 到站 300，计划离站 320；目标 390，需求恰好 70=上限。
	s := buildTwoAtControl2(t, testConfig(70, 200, 100000), 270, 300)
	info, err := s.Intervene(2, 2, ActionHold)
	mustOK(t, err, "intervene")
	if info.Action != ActionHold || info.HoldSec != 70 || info.Reason != ReasonHoldCap {
		t.Fatalf("期望扣车 70 且 hold-capped，得到 action=%s hold=%d reason=%q",
			info.Action, info.HoldSec, info.Reason)
	}
	if info.Departure != 390 {
		t.Fatalf("期望离站 390，得到 %d", info.Departure)
	}
	if pa := s.PlanArrival(s.trips[2], 3); pa != 420 {
		t.Fatalf("期望站3计划到站 420（真实到站300+扣车70+行驶50），得到 %d", pa)
	}
}

// 推后后离站时刻恰好等于最晚允许离站，允许扣车（取等不超）。
func TestHoldDepartureExactlyLatest(t *testing.T) {
	// 前车站2 离站 290；后车到站 319，计划离站 339，目标 390，需求 51；
	// 容忍量 71 => 最晚 = 319+71 = 390，恰好等于推后离站。
	s := buildTwoAtControl2(t, testConfig(200, 71, 100000), 270, 319)
	info, err := s.Intervene(2, 2, ActionHold)
	mustOK(t, err, "hold")
	if info.Action != ActionHold || info.HoldSec != 51 || info.Departure != 390 {
		t.Fatalf("期望恰好按最晚离站扣车 51，得到 action=%s hold=%d dep=%d",
			info.Action, info.HoldSec, info.Departure)
	}
	if info.Reason != ReasonNone {
		t.Fatalf("取等不应降级，得到原因 %q", info.Reason)
	}
}

// 扣车导致推后离站晚于最晚允许离站：改判跳站且成功。
func TestHoldLateFallbackSkip(t *testing.T) {
	s := buildTwoAtControl2(t, testConfig(200, 10, 100000), 270, 300)
	info, err := s.Intervene(2, 2, ActionHold)
	mustOK(t, err, "intervene")
	if info.Action != ActionSkip {
		t.Fatalf("期望改判跳站，得到 action=%s reason=%q", info.Action, info.Reason)
	}
	sk := s.rec(2, 3)
	if sk == nil || !sk.skipped {
		t.Fatalf("站3应已登记为跳过站")
	}
	// 被跳过站停站归零：到站=离站。
	if sk.arrival != sk.departure {
		t.Fatalf("跳过站停站应归零，到站 %d 离站 %d", sk.arrival, sk.departure)
	}
}

// 跳站途中存在下车请求：跳站被拒，只记录到站，且不留任何跳站痕迹。
func TestSkipRejectedByAlightRequest(t *testing.T) {
	s := buildTwoAtControl2(t, testConfig(200, 10, 100000), 270, 300)
	mustOK(t, s.RegisterAlight(2, 3, 305), "登记下车请求")
	info, err := s.Intervene(2, 2, ActionSkip)
	mustOK(t, err, "intervene")
	if info.Action != ActionNone || info.Reason != ReasonAlightRequest {
		t.Fatalf("期望因下车请求拒绝跳站，得到 action=%s reason=%q", info.Action, info.Reason)
	}
	if s.rec(2, 3) != nil {
		t.Fatalf("被拒跳站不得生成跳过站记录")
	}
	if s.trips[2].skipUsed {
		t.Fatalf("被拒跳站不得消耗一次跳站额度")
	}
}

// 大间隔恰等于两倍目标间隔（200）不插车。
func TestGapExactlyDoubleNoInsert(t *testing.T) {
	s := buildTwoAtControl2(t, testConfig(60, 40, 100000), 270, 470)
	info, err := s.Query(2, 2)
	mustOK(t, err, "query")
	if info.Verdict != VerdictNormal {
		t.Fatalf("两倍取等应正常，得到 %q", info.Verdict)
	}
	if len(s.pool) != 0 {
		t.Fatalf("不应插入备车")
	}
}

// 大间隔严格大于两倍：插入池中车次号最小的备车，时刻为前车到站+目标间隔。
func TestGapInsertSmallestBackup(t *testing.T) {
	s, err := New(testConfig(60, 40, 100000))
	mustOK(t, err, "New")
	mustOK(t, s.RegisterDriver(1), "d1")
	mustOK(t, s.RegisterDriver(2), "d2")
	mustOK(t, s.RegisterDriver(9), "d9")
	mustOK(t, s.RegisterDriver(7), "d7")
	mustOK(t, s.ScheduleTrip(1, 1, 0), "s1")
	mustOK(t, s.ScheduleTrip(2, 2, 10), "s2")
	mustOK(t, s.AddBackup(90, 9, 11), "备车90")
	mustOK(t, s.AddBackup(70, 7, 12), "备车70")
	mustOK(t, s.ReportArrival(1, 1, 220), "a")
	mustOK(t, s.ReportArrival(2, 1, 250), "b")
	mustOK(t, s.ReportArrival(1, 2, 270), "c")
	mustOK(t, s.ReportArrival(2, 2, 480), "d")
	info, err := s.Query(2, 2)
	mustOK(t, err, "query")
	if info.Verdict != VerdictGap || info.Action != ActionInsert || info.Inserted != 70 {
		t.Fatalf("期望插入最小备车70，得到 verdict=%s action=%s insert=%d",
			info.Verdict, info.Action, info.Inserted)
	}
	if s.orderTrips[s.orderPos(s.trips[70])+1].id != 2 {
		t.Fatalf("插入车应位于车次1与车次2之间")
	}
	ins, err := s.Query(70, 2)
	mustOK(t, err, "query inserted")
	if ins.Arrival != 480 || ins.Departure != 500 {
		t.Fatalf("备车插入时刻不得早于当前时钟 480，得到 %d/%d", ins.Arrival, ins.Departure)
	}
	if _, err := s.Query(70, 0); err != nil {
		t.Fatalf("插入前站点应可查询（无记录），得到 %v", err)
	}
}

// 大间隔但备车池为空：只记录事件与 pool-empty 原因。
func TestGapEmptyPool(t *testing.T) {
	s := buildTwoAtControl2(t, testConfig(60, 40, 100000), 270, 480)
	info, err := s.Query(2, 2)
	mustOK(t, err, "query")
	if info.Verdict != VerdictGap || info.Reason != ReasonPoolEmpty {
		t.Fatalf("期望大间隔+空池记录，得到 verdict=%s reason=%q",
			info.Verdict, info.Reason)
	}
}

// 扣车使连续在岗时长恰好等于上限：允许（取等不超）。
func TestHoldDutyExactlyLimitAllowed(t *testing.T) {
	// 车次2 首发 50，站2 到站 310（串车），计划离站 330；前车离站 290，
	// 目标 390，需求 60；扣后离站 390，连续在岗 390-50=340，恰等于上限。
	cfg := testConfig(200, 200, 340)
	s, err := New(cfg)
	mustOK(t, err, "New")
	mustOK(t, s.RegisterDriver(1), "d1")
	mustOK(t, s.RegisterDriver(2), "d2")
	mustOK(t, s.ScheduleTrip(1, 1, 0), "s1")
	mustOK(t, s.ScheduleTrip(2, 2, 50), "s2")
	mustOK(t, s.ReportArrival(1, 1, 220), "a")
	mustOK(t, s.ReportArrival(1, 2, 270), "b")
	mustOK(t, s.ReportArrival(2, 1, 280), "c")
	mustOK(t, s.ReportArrival(2, 2, 310), "d")
	info, err := s.Intervene(2, 2, ActionHold)
	mustOK(t, err, "hold")
	if info.Action != ActionHold || info.HoldSec != 60 || info.Departure != 390 {
		t.Fatalf("工时取等应允许扣车 60（在岗 390-50=340），得到 action=%s hold=%d dep=%d reason=%q",
			info.Action, info.HoldSec, info.Departure, info.Reason)
	}
}

// 扣车会使连续在岗超过上限：拒绝、降级为不干预，且不改判跳站。
func TestHoldDutyOverLimitDowngrade(t *testing.T) {
	cfg := testConfig(200, 200, 339) // 容忍足够大：若误因最晚离站改判跳站会暴露
	s, err := New(cfg)
	mustOK(t, err, "New")
	mustOK(t, s.RegisterDriver(1), "d1")
	mustOK(t, s.RegisterDriver(2), "d2")
	mustOK(t, s.ScheduleTrip(1, 1, 0), "s1")
	mustOK(t, s.ScheduleTrip(2, 2, 50), "s2")
	mustOK(t, s.ReportArrival(1, 1, 220), "a")
	mustOK(t, s.ReportArrival(1, 2, 270), "b")
	mustOK(t, s.ReportArrival(2, 1, 280), "c")
	mustOK(t, s.ReportArrival(2, 2, 310), "d")
	info, err := s.Intervene(2, 2, ActionHold)
	mustOK(t, err, "hold 不是错误")
	if info.Action != ActionNone || info.Reason != ReasonDutyLimit {
		t.Fatalf("期望工时超限降级，得到 action=%s reason=%q", info.Action, info.Reason)
	}
	if s.rec(2, 3) != nil {
		t.Fatalf("工时降级不得改判跳站")
	}
}

// 乱序上报（站序倒退 / 跨站）与重复上报被拒绝且不留痕。
func TestOutOfOrderAndDuplicateNoTrace(t *testing.T) {
	s, err := New(testConfig(60, 40, 100000))
	mustOK(t, err, "New")
	mustOK(t, s.RegisterDriver(1), "d1")
	mustOK(t, s.ScheduleTrip(1, 1, 0), "s1")

	clockBefore := s.Clock()
	wantErr(t, s.ReportArrival(1, 0, 100), ErrDuplicateReport, "重复站0")
	wantErr(t, s.ReportArrival(1, 0, 50), ErrDuplicateReport, "重复站0(更早)")
	if s.Clock() != clockBefore {
		t.Fatalf("被拒操作不得推进时钟")
	}

	wantErr(t, s.ReportArrival(1, 3, 300), ErrOutOfOrder, "跨站直报站3")
	if s.rec(1, 3) != nil {
		t.Fatalf("乱序上报不得生成记录")
	}

	mustOK(t, s.ReportArrival(1, 1, 220), "正常站1")
	wantErr(t, s.ReportArrival(1, 1, 225), ErrDuplicateReport, "重复站1")
	wantErr(t, s.ReportArrival(1, 0, 230), ErrDuplicateReport, "站0已存在记录=>重复优先")
	if r := s.rec(1, 1); r.arrival != 220 {
		t.Fatalf("重复/乱序不得改变到站时刻，得到 %d", r.arrival)
	}
	if s.trips[1].lastReport != 1 {
		t.Fatalf("被拒操作不得改变上报游标")
	}

	// 时钟回退独立可区分。
	wantErr(t, s.ReportArrival(1, 2, 219), ErrClockRollback, "时钟回退")
}

// 错误优先级：参数非法最先；其后时钟回退、车次不存在、站点不存在、乱序等。
func TestErrorPrecedence(t *testing.T) {
	s, err := New(testConfig(60, 40, 100000))
	mustOK(t, err, "New")
	mustOK(t, s.RegisterDriver(1), "d1")
	mustOK(t, s.ScheduleTrip(1, 1, 100), "s1")

	wantErr(t, s.ReportArrival(-1, -1, -1), ErrInvalidArgument, "非法参数")
	wantErr(t, s.ReportArrival(1, 1, 50), ErrClockRollback, "时钟回退优先于乱序")
	wantErr(t, s.ReportArrival(999, 1, 100), ErrTripNotFound, "车次不存在")
	wantErr(t, s.ReportArrival(1, 99, 200), ErrStationNotFound, "站点不存在")
	wantErr(t, s.ReportArrival(1, 0, 200), ErrDuplicateReport, "重复优先于乱序")

	// 司机不存在。
	s2, _ := New(testConfig(60, 40, 100000))
	wantErr(t, s2.ScheduleTrip(1, 8, 0), ErrDriverNotFound, "司机不存在")
	// 未上报控制站就干预：不是串车。
	_, err = s.Intervene(1, 2, ActionHold)
	wantErr(t, err, ErrNotBunched, "未判串车")
}

// 相邻车次同站已冻结离站时刻差不为负（含扣车与备车插入）。
func TestDepartureOrderingNonNegative(t *testing.T) {
	s := buildTwoAtControl2(t, testConfig(200, 200, 100000), 270, 300)
	info, err := s.Intervene(2, 2, ActionHold)
	mustOK(t, err, "hold")
	if info.Action != ActionHold {
		t.Fatalf("前置扣车应成功，得到 %s/%q", info.Action, info.Reason)
	}
	for st := 0; st < 5; st++ {
		var prev int64
		for _, tr := range s.orderTrips {
			r := s.rec(tr.id, st)
			if r == nil || !r.finalized {
				continue
			}
			if r.departure < prev {
				t.Fatalf("站%d 离站次序为负：%d < %d", st, r.departure, prev)
			}
			prev = r.departure
		}
	}
}
