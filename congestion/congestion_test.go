package congestion_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"ontology/congestion"
)

// 收费时段两端：起点（含）计费；终点（不含）不计费、不登记，之后仍算首次。
func TestWindowBoundaries(t *testing.T) {
	s := newSvc()
	addStdZones(t, s)
	reg(t, s, "v1")
	if err := s.Enter("v1", "inner", atDay("2026-01-02", 900)); err != nil {
		t.Fatal(err)
	}
	r := mustQuery(t, s, "v1", "2026-01-02")
	if r.Payable != 90 || len(r.Lines) != 2 {
		t.Fatalf("at start: payable=%d lines=%d", r.Payable, len(r.Lines))
	}

	s2 := newSvc()
	addStdZones(t, s2)
	reg(t, s2, "v2")
	// 8:30 时 inner 尚未开窗（9:00 起）：inner 不计费也不登记；
	// 9:00 恰为 inner 起点（含），应被当作 inner 当日首次进入而计费。
	if err := s2.Enter("v2", "inner", atDay("2026-01-02", 830)); err != nil {
		t.Fatal(err)
	}
	if err := s2.Enter("v2", "inner", atDay("2026-01-02", 900)); err != nil {
		t.Fatal(err)
	}
	r2 := mustQuery(t, s2, "v2", "2026-01-02")
	// 8:30 仅 outer 计费(60)；9:00 inner 首次(30)、outer 已登记不重复。
	if r2.Payable != 90 || len(r2.Lines) != 2 || lineOf(r2, "inner") == nil {
		t.Fatalf("at end: payable=%d lines=%+v", r2.Payable, r2.Lines)
	}
}

// 恰好触及日封顶；封顶后的进入仍登记但计费为零。
func TestExactlyHitsCap(t *testing.T) {
	s := newSvc()
	addStdZones(t, s)
	if err := s.AddZone(congestion.Zone{ID: "solo", DailyFee: 30,
		Cells: cells("z"), StartHHMM: 8 * 60, EndHHMM: 20 * 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddZone(congestion.Zone{ID: "solo2", DailyFee: 40,
		Cells: cells("y"), StartHHMM: 8 * 60, EndHHMM: 20 * 60}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, "v")
	_ = s.Enter("v", "inner", atDay("2026-01-02", 900)) // 30+60=90
	_ = s.Enter("v", "solo", atDay("2026-01-02", 1130)) // 封顶行 10
	r := mustQuery(t, s, "v", "2026-01-02")
	if r.Payable != 100 {
		t.Fatalf("cap: %d", r.Payable)
	}
	last := r.Lines[len(r.Lines)-1]
	if last.Payable != 10 || last.Reason != "capped:daily_cap" {
		t.Fatalf("cap line: %+v", last)
	}
	_ = s.Enter("v", "solo2", atDay("2026-01-02", 1200))
	r = mustQuery(t, s, "v", "2026-01-02")
	l := r.Lines[len(r.Lines)-1]
	if r.Payable != 100 || l.Payable != 0 || l.Reason != "capped:daily_cap_already_reached" {
		t.Fatalf("post-cap: %+v", l)
	}
}

// 三类资格同时存在：残障压过新能源与居民，只留残障痕迹。
func TestThreeQualificationsPrecedence(t *testing.T) {
	s := newSvc()
	addStdZones(t, s)
	reg(t, s, "v")
	_ = s.AddQualification("v", congestion.Qualification{Kind: congestion.Resident,
		ZoneID: "inner", Discount: 50, Start: atDay("2026-01-01", 0), End: atDay("2026-02-01", 0)}, atDay("2026-01-01", 0))
	_ = s.AddQualification("v", congestion.Qualification{Kind: congestion.NewEnergy,
		Start: atDay("2026-01-01", 0), End: atDay("2026-02-01", 0)}, atDay("2026-01-01", 1))
	_ = s.AddQualification("v", congestion.Qualification{Kind: congestion.Disabled,
		Start: atDay("2026-01-01", 0), End: atDay("2026-02-01", 0)}, atDay("2026-01-01", 2))
	_ = s.Enter("v", "inner", atDay("2026-01-02", 900))
	r := mustQuery(t, s, "v", "2026-01-02")
	if r.Payable != 0 {
		t.Fatalf("disabled zeroes all: %d", r.Payable)
	}
	for _, ln := range r.Lines {
		if ln.Reason != "exempt:disabled" {
			t.Fatalf("only disabled trace allowed, got %q", ln.Reason)
		}
	}
}

// 折扣向上取整后逐笔封顶：inner 折后 15，outer 60，solo 被压到 25。
func TestDiscountRoundingHitsCap(t *testing.T) {
	s := newSvc()
	addStdZones(t, s)
	if err := s.AddZone(congestion.Zone{ID: "solo", DailyFee: 30,
		Cells: cells("z"), StartHHMM: 8 * 60, EndHHMM: 20 * 60}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, "v")
	_ = s.AddQualification("v", congestion.Qualification{Kind: congestion.Resident,
		ZoneID: "inner", Discount: 50, Start: atDay("2026-01-01", 0), End: atDay("2026-02-01", 0)}, atDay("2026-01-01", 0))
	_ = s.Enter("v", "inner", atDay("2026-01-02", 900))
	_ = s.Enter("v", "solo", atDay("2026-01-02", 1000))
	r := mustQuery(t, s, "v", "2026-01-02")
	if r.Payable != 100 {
		t.Fatalf("payable=%d", r.Payable)
	}
	il, sl := lineOf(r, "inner"), lineOf(r, "solo")
	if il == nil || il.Payable != 15 || il.Reason != "resident_discount" {
		t.Fatalf("inner %+v", il)
	}
	if sl == nil || sl.Payable != 25 {
		t.Fatalf("solo %+v", sl)
	}
}

// 同一分钟内先进内层再进外层，与反向进入，计费一致且不重复。
func TestSameMinuteNestedOrder(t *testing.T) {
	for _, order := range [][]string{{"inner", "outer"}, {"outer", "inner"}} {
		s := newSvc()
		addStdZones(t, s)
		reg(t, s, "v")
		_ = s.Enter("v", order[0], atDay("2026-01-02", 1000))
		_ = s.Enter("v", order[1], atDay("2026-01-02", 1000).Add(30*time.Second))
		r := mustQuery(t, s, "v", "2026-01-02")
		if r.Payable != 90 || len(r.Lines) != 2 {
			t.Fatalf("order=%v payable=%d lines=%d", order, r.Payable, len(r.Lines))
		}
	}
}

var loc = time.FixedZone("CST", 8*3600)

func atDay(day string, hm int) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04",
		fmt.Sprintf("%s %02d:%02d", day, hm/100, hm%100), loc)
	return t
}

func cells(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}
func newSvc() *congestion.Service {
	return congestion.New(congestion.Config{
		Location:      loc,
		DailyCap:      100,
		RetroDays:     3,
		DiscountBasis: 100,
	})
}

func addStdZones(t testing.TB, s *congestion.Service) {
	t.Helper()
	zones := []congestion.Zone{
		{ID: "outer", DailyFee: 60, Cells: cells("a", "b", "c", "d"), StartHHMM: 8 * 60, EndHHMM: 20 * 60},
		{ID: "inner", DailyFee: 30, Cells: cells("a", "b"), StartHHMM: 9 * 60, EndHHMM: 18 * 60},
	}
	for _, z := range zones {
		if err := s.AddZone(z); err != nil {
			t.Fatalf("add zone %s: %v", z.ID, err)
		}
	}
}

func reg(t testing.TB, s *congestion.Service, id string) {
	t.Helper()
	if err := s.RegisterVehicle(id, id+"-plate", atDay("2026-01-01", 0)); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func mustQuery(t *testing.T, s *congestion.Service, vid, day string) congestion.DayReport {
	t.Helper()
	r, err := s.Query(vid, day)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return r
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func lineOf(r congestion.DayReport, zone string) *congestion.ChargeLine {
	for i := range r.Lines {
		if r.Lines[i].ZoneID == zone {
			return &r.Lines[i]
		}
	}
	return nil
}

// 追溯登记恰在追溯期最后一刻接受并退款；再晚一刻拒绝且无任何状态变化。
func TestRetroactiveBoundary(t *testing.T) {
	mk := func() *congestion.Service {
		s := newSvc()
		addStdZones(t, s)
		_ = s.AddZone(congestion.Zone{ID: "solo", DailyFee: 30,
			Cells: cells("z"), StartHHMM: 8 * 60, EndHHMM: 20 * 60})
		reg(t, s, "v")
		_ = s.Enter("v", "solo", atDay("2026-01-01", 1900))
		return s
	}
	lastMoment := atDay("2026-01-02", 0).Add(72 * time.Hour) // 1 日日结束 +3 天
	if got := lastMoment.Format("2006-01-02 15:04"); got != "2026-01-05 00:00" {
		t.Fatalf("arithmetic: %s", got)
	}
	s := mk()
	err := s.AddQualification("v", congestion.Qualification{Kind: congestion.NewEnergy,
		Start: atDay("2026-01-01", 0), End: atDay("2026-02-01", 0)}, lastMoment)
	if err != nil {
		t.Fatalf("last moment should be accepted: %v", err)
	}
	r := mustQuery(t, s, "v", "2026-01-01")
	if r.Payable != 0 || len(r.Adjustments) != 2 ||
		r.Adjustments[0].Amount != 30 || r.Adjustments[1].Amount != -30 {
		t.Fatalf("refund payable=%d adj=%+v", r.Payable, r.Adjustments)
	}

	s2 := mk()
	err = s2.AddQualification("v", congestion.Qualification{Kind: congestion.NewEnergy,
		Start: atDay("2026-01-01", 0), End: atDay("2026-02-01", 0)}, lastMoment.Add(time.Second))
	wantErr(t, err, congestion.ErrTooLate)
	r2 := mustQuery(t, s2, "v", "2026-01-01")
	if r2.Payable != 30 || len(r2.Adjustments) != 1 || r2.Adjustments[0].Amount != 30 {
		t.Fatalf("rejected op must not change state: %d %+v", r2.Payable, r2.Adjustments)
	}
}

// 车牌变更恰在进入时刻：该次进入归新车牌；资格仍随车。
func TestPlateChangeAtEntryInstant(t *testing.T) {
	s := newSvc()
	addStdZones(t, s)
	reg(t, s, "v")
	moment := atDay("2026-01-02", 900)
	if err := s.ChangePlate("v", "NEW", moment); err != nil {
		t.Fatal(err)
	}
	if err := s.Enter("v", "inner", moment); err != nil {
		t.Fatal(err)
	}
	es := s.VehicleEntries("v")
	if len(es) != 1 || es[0].Plate != "NEW" {
		t.Fatalf("entry should belong to NEW: %+v", es)
	}
	r := mustQuery(t, s, "v", "2026-01-02")
	if r.Payable != 90 {
		t.Fatalf("payable %d", r.Payable)
	}
}

// 争议期间追溯登记与进入不改冻结值；关闭后一次性体现为单独一笔调整。
func TestDisputeFreezeAndClose(t *testing.T) {
	s := newSvc()
	addStdZones(t, s)
	_ = s.AddZone(congestion.Zone{ID: "solo", DailyFee: 30,
		Cells: cells("z"), StartHHMM: 8 * 60, EndHHMM: 20 * 60})
	reg(t, s, "v")
	_ = s.Enter("v", "solo", atDay("2026-01-01", 900))
	if err := s.OpenDispute("v", "2026-01-01", atDay("2026-01-02", 800)); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.OpenDispute("v", "2026-01-01", atDay("2026-01-02", 830)), congestion.ErrDisputeExists)
	// 冻结期内收到追溯豁免：照常登记，但冻结值保持 30。
	if err := s.AddQualification("v", congestion.Qualification{Kind: congestion.NewEnergy,
		Start: atDay("2026-01-01", 0), End: atDay("2026-02-01", 0)}, atDay("2026-01-02", 900)); err != nil {
		t.Fatal(err)
	}
	r := mustQuery(t, s, "v", "2026-01-01")
	// 冻结值固定 30；入账调整是冻结前已发生的台账，冻结后不再追加。
	if !r.Frozen || r.Payable != 30 || len(r.Adjustments) != 1 {
		t.Fatalf("frozen: %+v", r)
	}
	if err := s.CloseDispute("v", "2026-01-01", atDay("2026-01-03", 900)); err != nil {
		t.Fatal(err)
	}
	r = mustQuery(t, s, "v", "2026-01-01")
	closeAdjs := 0
	for _, x := range r.Adjustments {
		if x.Reason == "dispute_close:recompute" {
			closeAdjs++
		}
	}
	if r.Payable != 0 || closeAdjs != 1 {
		t.Fatalf("close payable=%d adj=%+v", r.Payable, r.Adjustments)
	}
	var found *congestion.Adjustment
	for i := range r.Adjustments {
		if r.Adjustments[i].Reason == "dispute_close:recompute" {
			found = &r.Adjustments[i]
		}
	}
	if found == nil || found.Amount != -30 || found.Before != 30 || found.After != 0 {
		t.Fatalf("close adjustment: %+v", r.Adjustments)
	}
	wantErr(t, s.CloseDispute("v", "2026-01-01", atDay("2026-01-03", 1000)), congestion.ErrNoDispute)
}

// 错误分类与“只报最靠前一类”及拒绝不改时钟。
func TestErrorOrdering(t *testing.T) {
	s := newSvc()
	// 参数非法优先于一切
	wantErr(t, s.Enter("", "nope", time.Time{}), congestion.ErrInvalid)
	// 合法参数后：时钟回退先于车辆/区域不存在
	reg(t, s, "v")
	good := atDay("2026-01-05", 900)
	_ = s.Enter("v", "nope-zone", good) // 车辆存在、区域不存在
	// 上一步被拒绝（区域不存在）不应推进时钟；但注册把时钟推到 1/1。
	wantErr(t, s.Enter("ghost", "nope-zone", atDay("2025-12-31", 0)), congestion.ErrClockBack)
	wantErr(t, s.Enter("ghost", "zz", good), congestion.ErrVehicleNotFound)
	wantErr(t, s.Enter("v", "zz", good), congestion.ErrZoneNotFound)

	// 同类资格区间重叠
	addStdZones(t, s)
	// 嵌套关系不合法：与已有 inner(a,b) 相交（共享 a）但互不包含
	err := s.AddZone(congestion.Zone{ID: "bad", DailyFee: 10,
		Cells: cells("a", "x"), StartHHMM: 8 * 60, EndHHMM: 20 * 60})
	wantErr(t, err, congestion.ErrZonesOverlap)
	q := congestion.Qualification{Kind: congestion.Disabled,
		Start: atDay("2026-01-01", 0), End: atDay("2026-01-10", 0)}
	if err := s.AddQualification("v", q, good); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.AddQualification("v", congestion.Qualification{Kind: congestion.Disabled,
		Start: atDay("2026-01-09", 0), End: atDay("2026-01-20", 0)}, atDay("2026-01-05", 1000)),
		congestion.ErrIntervalOverlap)
	// 首尾相接（左闭右开）不重叠
	if err := s.AddQualification("v", congestion.Qualification{Kind: congestion.Disabled,
		Start: atDay("2026-01-10", 0), End: atDay("2026-01-20", 0)}, atDay("2026-01-05", 1100)); err != nil {
		t.Fatalf("abutting intervals must be allowed: %v", err)
	}
}
