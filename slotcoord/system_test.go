package slotcoord

import (
	"sync"
	"testing"
)

// baseConfig 返回标准测试配置: 4 周, 每周 100 刻度, 第 w 周结束于
// 100*(w+1), 登记窗口 [周结束, 周结束+50](终点取闭), 申请截止 50,
// 返还截止 200, 航季结束 400。
func baseConfig() Config {
	return Config{
		Weeks:                4,
		MinSeriesWeeks:       1,
		Capacity:             2,
		HistoricThresholdPct: 75,
		NewcomerThreshold:    2,
		SeasonStart:          0,
		WeekLength:           100,
		ApplicationDeadline:  50,
		ReturnDeadline:       200,
		RegistrationWindow:   50,
	}
}

func newTestSystem(t *testing.T, cfg Config, historic []Eligibility) *System {
	t.Helper()
	s, err := NewSystem(cfg, historic)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func mustSubmit(t *testing.T, s *System, now int64, airline string, weekday, hour, start, end int) int {
	t.Helper()
	id, err := s.SubmitRequest(now, Request{
		Airline: airline, Weekday: weekday, Hour: hour, StartWeek: start, EndWeek: end,
	})
	if err != nil {
		t.Fatalf("SubmitRequest(%s): %v", airline, err)
	}
	return id
}

func mustClose(t *testing.T, s *System, now int64) {
	t.Helper()
	if err := s.CloseApplications(now); err != nil {
		t.Fatalf("CloseApplications: %v", err)
	}
}

func mustSettle(t *testing.T, s *System, now int64) {
	t.Helper()
	if err := s.SettleSeason(now); err != nil {
		t.Fatalf("SettleSeason: %v", err)
	}
}

// allocOnly 提交给定申请并截止, 返回快照。
func allocOnly(t *testing.T, s *System, submits func()) Snapshot {
	t.Helper()
	submits()
	mustClose(t, s, 50)
	return s.Snapshot()
}

func hasEligibility(list []Eligibility, e Eligibility) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}

func seriesByHolder(snap Snapshot, holder string) []SeriesView {
	var out []SeriesView
	for _, sv := range snap.Series {
		if sv.Holder == holder {
			out = append(out, sv)
		}
	}
	return out
}

// 使用率恰等于达标比例视为达标; 低一个执行单位则不达标。
func TestUsageExactlyAtThresholdAndOneBelow(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 3)
	mustSubmit(t, s, 11, "B", 0, 1, 0, 3)
	mustClose(t, s, 50)
	snap := s.Snapshot()
	if len(snap.Series) != 2 {
		t.Fatalf("期望 2 个系列, 实际 %d", len(snap.Series))
	}
	aID, bID := snap.Series[0].ID, snap.Series[1].ID
	// A 执行 4 周中的 3 周: 3/4 = 75% 恰等于达标比例; 第 3 周不登记。
	// B 只执行 2 周: 2/4 = 50% 低于达标比例; 第 2 周登记为未执行, 第 3 周不登记。
	regs := []struct {
		now      int64
		airline  string
		id       int
		week     int
		executed bool
	}{
		{100, "A", aID, 0, true},
		{110, "B", bID, 0, true},
		{200, "A", aID, 1, true},
		{210, "B", bID, 1, true},
		{300, "A", aID, 2, true},
		{310, "B", bID, 2, false},
	}
	for _, r := range regs {
		if err := s.RegisterExecution(r.now, r.airline, r.id, r.week, r.executed); err != nil {
			t.Fatalf("%s 登记第 %d 周: %v", r.airline, r.week, err)
		}
	}
	mustSettle(t, s, 400)
	elig := s.Eligibility()
	if !hasEligibility(elig, Eligibility{Airline: "A", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 3}) {
		t.Fatalf("A 恰等于达标比例应获得历史资格, 实际 %v", elig)
	}
	if hasEligibility(elig, Eligibility{Airline: "B", Weekday: 0, Hour: 1, StartWeek: 0, EndWeek: 3}) {
		t.Fatalf("B 低一个执行单位不应获得历史资格, 实际 %v", elig)
	}
}

// 返还截止前返还的周不计入分母; 截止后返还的周计入分母而不计入分子。
func TestReturnBeforeAndAfterDeadline(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 3)
	mustClose(t, s, 50)
	id := s.Snapshot().Series[0].ID
	// 第 0 周在返还截止(200, 含)前返还: 不计入分母。
	if err := s.ReturnWeeks(150, "A", id, []int{0}); err != nil {
		t.Fatalf("截止前返还: %v", err)
	}
	// 第 1 周在返还截止后返还: 计入分母, 不计入分子。
	if err := s.ReturnWeeks(250, "A", id, []int{1}); err != nil {
		t.Fatalf("截止后返还: %v", err)
	}
	if err := s.RegisterExecution(300, "A", id, 2, true); err != nil {
		t.Fatalf("登记第 2 周: %v", err)
	}
	if err := s.RegisterExecution(400, "A", id, 3, true); err != nil {
		t.Fatalf("登记第 3 周: %v", err)
	}
	executed, planned, err := s.UsageOf(id)
	if err != nil {
		t.Fatalf("UsageOf: %v", err)
	}
	if executed != 2 || planned != 3 {
		t.Fatalf("期望 分子=2 分母=3, 实际 %d/%d", executed, planned)
	}
}

// 被认可的豁免周既不计入分子也不计入分母。
func TestExemptWeeksExcluded(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 2)
	mustClose(t, s, 50)
	id := s.Snapshot().Series[0].ID
	if err := s.RegisterExemption(120, "A", id, 0); err != nil {
		t.Fatalf("登记豁免: %v", err)
	}
	if err := s.RegisterExecution(200, "A", id, 1, true); err != nil {
		t.Fatalf("登记第 1 周: %v", err)
	}
	// 第 2 周不登记, 按未执行处理。
	executed, planned, err := s.UsageOf(id)
	if err != nil {
		t.Fatalf("UsageOf: %v", err)
	}
	if executed != 1 || planned != 2 {
		t.Fatalf("豁免周应同时不计入分子分母, 期望 1/2, 实际 %d/%d", executed, planned)
	}
}

// 分母为零(全部周在截止前返还)视为达标。
func TestZeroDenominatorEligible(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 1)
	mustClose(t, s, 50)
	id := s.Snapshot().Series[0].ID
	if err := s.ReturnWeeks(150, "A", id, []int{0, 1}); err != nil {
		t.Fatalf("返还全部周: %v", err)
	}
	executed, planned, err := s.UsageOf(id)
	if err != nil {
		t.Fatalf("UsageOf: %v", err)
	}
	if executed != 0 || planned != 0 {
		t.Fatalf("期望分母为零, 实际 %d/%d", executed, planned)
	}
	mustSettle(t, s, 400)
	if !hasEligibility(s.Eligibility(), Eligibility{Airline: "A", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 1}) {
		t.Fatalf("分母为零应视为达标, 实际 %v", s.Eligibility())
	}
}

// 剩余容量为奇数时新进入者保留额为一半向下取整: 容量 3 保留 1,
// 无新进入者时保留额被搁浅, 恰等于上限后不可再分配。
func TestNewcomerReserveRoundingDown(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 3
	cfg.NewcomerThreshold = 1
	// X 通过历史资格已持有 1 个系列, 达到阈值 1, 不是新进入者。
	historic := []Eligibility{{Airline: "X", Weekday: 0, Hour: 9, StartWeek: 0, EndWeek: 0}}
	s := newTestSystem(t, cfg, historic)
	mustSubmit(t, s, 10, "X", 0, 9, 0, 0) // 历史申请, 占据另一小时段
	mustSubmit(t, s, 11, "X", 0, 0, 0, 0)
	mustSubmit(t, s, 12, "X", 0, 0, 0, 0)
	mustSubmit(t, s, 13, "X", 0, 0, 0, 0)
	mustClose(t, s, 50)
	snap := s.Snapshot()
	// 保留额 = floor(3/2) = 1 被搁浅: 4 个申请中 1 个历史 + 2 个普通被满足,
	// 第 4 个进入等候名单, 单元格剩余容量恰为保留额 1。
	if got := len(seriesByHolder(snap, "X")); got != 3 {
		t.Fatalf("期望 X 持有 3 个系列, 实际 %d", got)
	}
	if rem := s.Remaining(0, 0, 0); rem != 1 {
		t.Fatalf("保留额向下取整应为 1 并被搁浅, 实际剩余 %d", rem)
	}
	if len(snap.Waitlists) != 1 || len(snap.Waitlists[0].Requests) != 1 {
		t.Fatalf("期望 1 个等候申请, 实际 %+v", snap.Waitlists)
	}
}

// 新进入者可以动用保留额。
func TestNewcomerConsumesReserve(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 3
	cfg.NewcomerThreshold = 1
	historic := []Eligibility{{Airline: "X", Weekday: 0, Hour: 9, StartWeek: 0, EndWeek: 0}}
	s := newTestSystem(t, cfg, historic)
	mustSubmit(t, s, 10, "X", 0, 9, 0, 0) // 历史申请
	mustSubmit(t, s, 11, "X", 0, 0, 0, 0)
	mustSubmit(t, s, 12, "X", 0, 0, 0, 0)
	mustSubmit(t, s, 13, "X", 0, 0, 0, 0)
	mustSubmit(t, s, 14, "Y", 0, 0, 0, 0) // Y 持有 0 < 1, 是新进入者
	mustClose(t, s, 50)
	snap := s.Snapshot()
	// Y 在第二轮动用保留额; X 的三个普通申请中两个在第三轮被满足。
	if got := len(seriesByHolder(snap, "Y")); got != 1 {
		t.Fatalf("新进入者 Y 应通过保留额获得 1 个系列, 实际 %d", got)
	}
	if got := len(seriesByHolder(snap, "X")); got != 3 {
		t.Fatalf("期望 X 持有 3 个系列, 实际 %d", got)
	}
	if rem := s.Remaining(0, 0, 0); rem != 0 {
		t.Fatalf("容量应被恰好分完, 实际剩余 %d", rem)
	}
	if len(snap.Waitlists) != 1 || len(snap.Waitlists[0].Requests) != 1 {
		t.Fatalf("期望 1 个等候申请, 实际 %+v", snap.Waitlists)
	}
}

// 新进入者阈值: 持有系列数恰等于阈值时不算新进入者, 少一个才算。
func TestNewcomerThresholdExactlyHeld(t *testing.T) {
	setup := func(threshold int) *System {
		cfg := baseConfig()
		cfg.Capacity = 2
		cfg.NewcomerThreshold = threshold
		// X 与 Z 各通过历史资格持有 2 个系列。
		historic := []Eligibility{
			{Airline: "X", Weekday: 0, Hour: 8, StartWeek: 0, EndWeek: 0},
			{Airline: "X", Weekday: 0, Hour: 9, StartWeek: 0, EndWeek: 0},
			{Airline: "Z", Weekday: 0, Hour: 10, StartWeek: 0, EndWeek: 0},
			{Airline: "Z", Weekday: 0, Hour: 11, StartWeek: 0, EndWeek: 0},
		}
		s := newTestSystem(t, cfg, historic)
		mustSubmit(t, s, 10, "X", 0, 8, 0, 0)
		mustSubmit(t, s, 11, "X", 0, 9, 0, 0)
		mustSubmit(t, s, 12, "Z", 0, 10, 0, 0)
		mustSubmit(t, s, 13, "Z", 0, 11, 0, 0)
		// 争夺小时段 (0,0): 容量 2, 无历史申请, 保留额 1。
		mustSubmit(t, s, 14, "X", 0, 0, 0, 0)
		mustSubmit(t, s, 15, "Z", 0, 0, 0, 0)
		mustClose(t, s, 50)
		return s
	}

	// 阈值 2: X 与 Z 持有数恰等于阈值, 都不是新进入者。
	// 保留额 1 被搁浅, 只有提交早的 X 在第三轮被满足。
	s := setup(2)
	snap := s.Snapshot()
	if got := len(snap.Waitlists); got != 1 {
		t.Fatalf("阈值恰等于持有数时不算新进入者, 期望 1 个等候名单, 实际 %d", got)
	}
	if rem := s.Remaining(0, 0, 0); rem != 1 {
		t.Fatalf("保留额应被搁浅为 1, 实际剩余 %d", rem)
	}
	// 阈值 3: X 与 Z 持有 2 < 3, 都是新进入者, X 先用保留额, Z 用普通容量。
	s = setup(3)
	snap = s.Snapshot()
	if len(snap.Waitlists) != 0 {
		t.Fatalf("持有数低于阈值时应为新进入者, 两者都应被满足, 等候名单 %+v", snap.Waitlists)
	}
	if rem := s.Remaining(0, 0, 0); rem != 0 {
		t.Fatalf("容量应恰好分完, 实际剩余 %d", rem)
	}
}

// 返还某周只释放该周, 并按等候名单次序分配。
func TestReturnReleasesOnlyThatWeekInOrder(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 1
	s := newTestSystem(t, cfg, nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 2)
	mustSubmit(t, s, 11, "B", 0, 0, 0, 2)
	mustSubmit(t, s, 12, "C", 0, 0, 0, 2)
	mustClose(t, s, 50)
	snap := s.Snapshot()
	if len(snap.Series) != 1 || snap.Series[0].Holder != "A" {
		t.Fatalf("容量 1 应只有 A 被满足, 实际 %+v", snap.Series)
	}
	if len(snap.Waitlists) != 1 || len(snap.Waitlists[0].Requests) != 2 {
		t.Fatalf("B 与 C 应进入等候名单, 实际 %+v", snap.Waitlists)
	}
	aID := snap.Series[0].ID
	// 只返还第 1 周: 仅该周容量释放, B 与 C 覆盖的第 0、2 周仍满, 无人被满足。
	if err := s.ReturnWeeks(150, "A", aID, []int{1}); err != nil {
		t.Fatalf("返还第 1 周: %v", err)
	}
	if rem := s.Remaining(1, 0, 0); rem != 1 {
		t.Fatalf("第 1 周应被释放, 实际剩余 %d", rem)
	}
	if rem := s.Remaining(0, 0, 0); rem != 0 {
		t.Fatalf("第 0 周不应被释放, 实际剩余 %d", rem)
	}
	if rem := s.Remaining(2, 0, 0); rem != 0 {
		t.Fatalf("第 2 周不应被释放, 实际剩余 %d", rem)
	}
	if got := len(s.Snapshot().Series); got != 1 {
		t.Fatalf("只释放一周不应满足任何等候申请, 实际系列数 %d", got)
	}
	// 再返还第 0、2 周: 提交早的 B 先被满足, C 仍在等候。
	if err := s.ReturnWeeks(160, "A", aID, []int{0, 2}); err != nil {
		t.Fatalf("返还第 0、2 周: %v", err)
	}
	snap = s.Snapshot()
	if len(snap.Series) != 2 {
		t.Fatalf("B 应被满足, 实际系列数 %d", len(snap.Series))
	}
	holders := map[string]bool{}
	for _, sv := range snap.Series {
		holders[sv.Holder] = true
	}
	if !holders["B"] || holders["C"] {
		t.Fatalf("应按名单次序满足 B 而非 C, 实际持有者 %v", holders)
	}
	if len(snap.Waitlists) != 1 || len(snap.Waitlists[0].Requests) != 1 {
		t.Fatalf("C 应仍在等候名单, 实际 %+v", snap.Waitlists)
	}
}

// 交换后各自的历史使用率记在接收方名下; 已执行的周不可交换。
func TestSwapAttributesUsageToReceiver(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "X", 0, 0, 0, 1)
	mustSubmit(t, s, 11, "Y", 0, 1, 0, 1)
	mustClose(t, s, 50)
	snap := s.Snapshot()
	xID, yID := snap.Series[0].ID, snap.Series[1].ID
	// 任何周都未执行过, 可以交换。
	if err := s.SwapSeries(100, xID, yID); err != nil {
		t.Fatalf("交换: %v", err)
	}
	snap = s.Snapshot()
	if snap.Series[0].Holder != "Y" || snap.Series[1].Holder != "X" {
		t.Fatalf("交换后持有者应互换, 实际 %+v", snap.Series)
	}
	// 接收方 Y 让原 X 的系列全部执行; 接收方 X 让原 Y 的系列全部未执行。
	if err := s.RegisterExecution(110, "Y", xID, 0, true); err != nil {
		t.Fatalf("Y 登记: %v", err)
	}
	if err := s.RegisterExecution(120, "X", yID, 0, false); err != nil {
		t.Fatalf("X 登记: %v", err)
	}
	if err := s.RegisterExecution(200, "Y", xID, 1, true); err != nil {
		t.Fatalf("Y 登记: %v", err)
	}
	mustSettle(t, s, 400)
	elig := s.Eligibility()
	if !hasEligibility(elig, Eligibility{Airline: "Y", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 1}) {
		t.Fatalf("交换后使用率应记在接收方 Y 名下, 实际 %v", elig)
	}
	if hasEligibility(elig, Eligibility{Airline: "X", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 1}) {
		t.Fatalf("原持有者 X 不应再拥有该时段历史资格, 实际 %v", elig)
	}
}

// 已执行的周不可交换。
func TestSwapRejectedAfterExecution(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "X", 0, 0, 0, 1)
	mustSubmit(t, s, 11, "Y", 0, 1, 0, 1)
	mustClose(t, s, 50)
	snap := s.Snapshot()
	xID, yID := snap.Series[0].ID, snap.Series[1].ID
	if err := s.RegisterExecution(100, "X", xID, 0, true); err != nil {
		t.Fatalf("登记: %v", err)
	}
	if err := s.SwapSeries(120, xID, yID); err != ErrWeekExecuted {
		t.Fatalf("已执行的周不可交换, 期望 %v, 实际 %v", ErrWeekExecuted, err)
	}
}

// 登记窗口终点取闭: 窗口终点时刻可登记, 过了终点报登记超期。
func TestRegistrationWindowEndpointInclusive(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 0)
	mustSubmit(t, s, 11, "B", 0, 1, 0, 0)
	mustClose(t, s, 50)
	snap := s.Snapshot()
	aID, bID := snap.Series[0].ID, snap.Series[1].ID
	// 第 0 周结束于 100, 窗口终点 150(取闭)。
	if err := s.RegisterExecution(150, "A", aID, 0, true); err != nil {
		t.Fatalf("窗口终点应可登记, 实际 %v", err)
	}
	if err := s.RegisterExecution(151, "B", bID, 0, true); err != ErrRegistrationLate {
		t.Fatalf("窗口终点之后应报登记超期, 实际 %v", err)
	}
}

// 拒绝次序: 每一对相邻类别只报更靠前的一类。
func TestRejectionOrderAdjacentPairs(t *testing.T) {
	// 参数非法 > 时钟回退: 无效申请 + 回退的时刻。
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 0)
	if _, err := s.SubmitRequest(5, Request{Airline: "A", Weekday: 9, Hour: 0, StartWeek: 0, EndWeek: 0}); err != ErrParam {
		t.Fatalf("参数非法应先于时钟回退, 实际 %v", err)
	}
	// 时钟回退 > 公司或系列不存在: 回退时刻 + 不存在的系列。
	if err := s.ReturnWeeks(5, "A", 999, []int{0}); err != ErrClockRegression {
		t.Fatalf("时钟回退应先于不存在, 实际 %v", err)
	}
	// 公司或系列不存在 > 航季阶段不符: 航季已结算 + 不存在的系列。
	mustClose(t, s, 50)
	mustSettle(t, s, 400)
	if err := s.RegisterExecution(400, "A", 999, 0, true); err != ErrNotFound {
		t.Fatalf("不存在应先于航季已结算, 实际 %v", err)
	}
	// 航季阶段不符 > 登记超期: 已结算 + 已超窗。
	id := s.Snapshot().Series[0].ID
	if err := s.RegisterExecution(400, "A", id, 0, true); err != ErrSeasonSettled {
		t.Fatalf("航季已结算应先于登记超期, 实际 %v", err)
	}

	// 登记超期 > 重复登记: 同一周已登记, 再次登记时已过窗口。
	s2 := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s2, 10, "A", 0, 0, 0, 0)
	mustClose(t, s2, 50)
	id2 := s2.Snapshot().Series[0].ID
	if err := s2.RegisterExecution(150, "A", id2, 0, true); err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	if err := s2.RegisterExecution(200, "A", id2, 0, true); err != ErrRegistrationLate {
		t.Fatalf("登记超期应先于重复登记, 实际 %v", err)
	}
	// 重复登记(窗口终点内再次登记)。
	if err := s2.RegisterExecution(150, "A", id2, 0, false); err != ErrDuplicateRegistration {
		t.Fatalf("窗口内重复登记应报重复登记, 实际 %v", err)
	}
}

// firstError 对统一拒绝次序的每一对相邻类别。
func TestFirstErrorAdjacentPairs(t *testing.T) {
	order := []error{
		ErrParam, ErrClockRegression, ErrNotFound, ErrApplicationClosed,
		ErrRegistrationLate, ErrDuplicateRegistration, ErrCapacity,
	}
	for i := 0; i+1 < len(order); i++ {
		if got := firstError(order[i+1], order[i]); got != order[i] {
			t.Fatalf("第 %d 对相邻类别次序错误: %v", i, got)
		}
		if got := firstError(order[i], order[i+1]); got != order[i] {
			t.Fatalf("第 %d 对相邻类别次序错误: %v", i, got)
		}
	}
	// 航季阶段不符三类可区分且同位次。
	for _, phase := range []error{ErrApplicationClosed, ErrSeasonSettled, ErrWeekExecuted} {
		if got := firstError(phase, ErrNotFound); got != ErrNotFound {
			t.Fatalf("不存在应先于阶段不符(%v), 实际 %v", phase, got)
		}
		if got := firstError(ErrRegistrationLate, phase); got != phase {
			t.Fatalf("阶段不符(%v)应先于登记超期, 实际 %v", phase, got)
		}
	}
}

// 历史申请之间超过容量属参数非法, 且整个截止操作被拒绝、不改变状态。
func TestHistoricOverCapacityIsParam(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 1
	historic := []Eligibility{
		{Airline: "A", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 0},
		{Airline: "B", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 0},
	}
	s := newTestSystem(t, cfg, historic)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 0)
	mustSubmit(t, s, 11, "B", 0, 0, 0, 0)
	if err := s.CloseApplications(50); err != ErrParam {
		t.Fatalf("历史申请超容量应报参数非法, 实际 %v", err)
	}
	if snap := s.Snapshot(); snap.Closed || len(snap.Series) != 0 {
		t.Fatalf("被拒绝的截止操作不应改变状态, 实际 %+v", snap)
	}
}

// 截止后提交报已截止; 重复登记报重复登记; 被拒绝的操作不推进时钟。
func TestDeadlineDuplicateAndClock(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 1)
	if _, err := s.SubmitRequest(51, Request{Airline: "B", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 0}); err != ErrApplicationClosed {
		t.Fatalf("截止后提交应报已截止, 实际 %v", err)
	}
	mustClose(t, s, 50)
	id := s.Snapshot().Series[0].ID
	if err := s.RegisterExecution(120, "A", id, 0, true); err != nil {
		t.Fatalf("登记: %v", err)
	}
	// 时钟回退: 被拒绝, 不推进时钟。
	if err := s.ReturnWeeks(110, "A", id, []int{1}); err != ErrClockRegression {
		t.Fatalf("应报时钟回退, 实际 %v", err)
	}
	// 同一周只能登记一次。
	if err := s.RegisterExecution(140, "A", id, 0, false); err != ErrDuplicateRegistration {
		t.Fatalf("同一周重复登记应报重复登记, 实际 %v", err)
	}
	// 被拒绝的操作(回退的返还、重复登记)不推进时钟: t=130 仍被接受。
	if err := s.RegisterExemption(130, "A", id, 1); err != nil {
		t.Fatalf("被拒绝的操作不应推进时钟, t=130 应被接受, 实际 %v", err)
	}
}

// 结算后本航季不可再登记或返还。
func TestSettleLocksSeason(t *testing.T) {
	s := newTestSystem(t, baseConfig(), nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 1)
	mustClose(t, s, 50)
	id := s.Snapshot().Series[0].ID
	mustSettle(t, s, 400)
	if err := s.RegisterExecution(400, "A", id, 0, true); err != ErrSeasonSettled {
		t.Fatalf("结算后登记应报航季已结算, 实际 %v", err)
	}
	if err := s.ReturnWeeks(400, "A", id, []int{0}); err != ErrSeasonSettled {
		t.Fatalf("结算后返还应报航季已结算, 实际 %v", err)
	}
	if err := s.RegisterExemption(400, "A", id, 1); err != ErrSeasonSettled {
		t.Fatalf("结算后豁免应报航季已结算, 实际 %v", err)
	}
}

// 历史资格滚入下一航季: 达标公司下一航季对相同时段与周数范围的申请
// 享有历史优先权, 即使提交更晚也先被满足。
func TestRolloverHistoricPriority(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 1
	s := newTestSystem(t, cfg, nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 1)
	mustClose(t, s, 50)
	id := s.Snapshot().Series[0].ID
	if err := s.RegisterExecution(100, "A", id, 0, true); err != nil {
		t.Fatalf("登记: %v", err)
	}
	if err := s.RegisterExecution(200, "A", id, 1, true); err != nil {
		t.Fatalf("登记: %v", err)
	}
	mustSettle(t, s, 400)

	next := cfg
	next.SeasonStart = 1000
	next.ApplicationDeadline = 1050
	next.ReturnDeadline = 1200
	if err := s.StartNextSeason(1000, next); err != nil {
		t.Fatalf("StartNextSeason: %v", err)
	}
	// B 提交更早, 但 A 有历史优先权。
	mustSubmit(t, s, 1010, "B", 0, 0, 0, 1)
	mustSubmit(t, s, 1011, "A", 0, 0, 0, 1)
	mustClose(t, s, 1050)
	snap := s.Snapshot()
	if len(snap.Series) != 1 || snap.Series[0].Holder != "A" {
		t.Fatalf("历史优先权应先满足 A, 实际 %+v", snap.Series)
	}
	if len(snap.Waitlists) != 1 {
		t.Fatalf("B 应进入等候名单, 实际 %+v", snap.Waitlists)
	}
}

// 性能证明: 返还触发的等候名单分配开销不随机场内系列总数与航季总周数增长。
func TestReturnCostIndependentOfScale(t *testing.T) {
	build := func(weeks int, extraSeries int) *System {
		cfg := baseConfig()
		cfg.Weeks = weeks
		cfg.Capacity = 1
		cfg.WeekLength = 100
		cfg.ReturnDeadline = int64(weeks) * 100 // 返还总在截止前, 不影响本测试
		s := newTestSystem(t, cfg, nil)
		mustSubmit(t, s, 10, "A", 0, 0, 0, 2)
		mustSubmit(t, s, 10, "B", 0, 0, 0, 2)
		mustSubmit(t, s, 10, "C", 0, 0, 0, 2)
		// 大量无关系列: 其他小时段, 覆盖整个航季。
		for i := 0; i < extraSeries; i++ {
			hour := 1 + i%23
			weekday := 1 + i%6
			mustSubmit(t, s, 10, "X", weekday, hour, 0, weeks-1)
		}
		mustClose(t, s, 50)
		return s
	}
	returnAndCount := func(s *System) int64 {
		id := s.Snapshot().Series[0].ID
		before := s.Stats().WaitlistCellVisits
		// 返还第 0、1、2 周, 触发等候名单分配(B 被满足)。
		if err := s.ReturnWeeks(150, "A", id, []int{0, 1, 2}); err != nil {
			t.Fatalf("返还: %v", err)
		}
		return s.Stats().WaitlistCellVisits - before
	}
	small := build(4, 0)
	largeSeries := build(4, 200)
	largeWeeks := build(60, 200)
	gotSmall := returnAndCount(small)
	if got := returnAndCount(largeSeries); got != gotSmall {
		t.Fatalf("开销随系列总数增长: 小规模 %d, 多系列 %d", gotSmall, got)
	}
	if got := returnAndCount(largeWeeks); got != gotSmall {
		t.Fatalf("开销随航季周数增长: 小规模 %d, 多周数 %d", gotSmall, got)
	}
	// sanity: 等候名单分配确实发生(B 被满足)。
	if got := len(seriesByHolder(largeWeeks.Snapshot(), "B")); got != 1 {
		t.Fatalf("等候名单分配应发生, B 的系列数 %d", got)
	}
}

// 性能证明: 单个系列使用率计算的开销只随该系列周数增长。
func TestUsageCostOnlyOwnWeeks(t *testing.T) {
	build := func(extraSeries int) (*System, int) {
		cfg := baseConfig()
		s := newTestSystem(t, cfg, nil)
		mustSubmit(t, s, 10, "A", 0, 0, 0, 2)
		for i := 0; i < extraSeries; i++ {
			mustSubmit(t, s, 10, "X", 1+i%6, 1+i%23, 0, 3)
		}
		mustClose(t, s, 50)
		return s, s.Snapshot().Series[0].ID
	}
	count := func(s *System, id int) int64 {
		before := s.Stats().UsageWeekScans
		if _, _, err := s.UsageOf(id); err != nil {
			t.Fatalf("UsageOf: %v", err)
		}
		return s.Stats().UsageWeekScans - before
	}
	small, smallID := build(0)
	large, largeID := build(300)
	gotSmall, gotLarge := count(small, smallID), count(large, largeID)
	if gotSmall != 3 || gotLarge != 3 {
		t.Fatalf("使用率扫描周数应等于系列自身周数 3, 实际 %d 与 %d", gotSmall, gotLarge)
	}
}

// 并发调用等价于某个串行顺序: 不变式为任何单元格已分配数不超过容量。
func TestConcurrentOperations(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 2
	s := newTestSystem(t, cfg, nil)
	mustSubmit(t, s, 10, "A", 0, 0, 0, 3)
	mustSubmit(t, s, 11, "B", 0, 0, 0, 3)
	mustSubmit(t, s, 12, "C", 0, 0, 0, 3) // 容量 2, C 进入等候名单
	mustClose(t, s, 50)
	snap := s.Snapshot()
	aID, bID := snap.Series[0].ID, snap.Series[1].ID

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// 同一时刻并发出现在周 0 的登记与返还, 结果由串行化决定。
			_ = s.RegisterExecution(150, "A", aID, 0, g%2 == 0)
			_ = s.RegisterExecution(150, "B", bID, 0, g%2 == 1)
			_ = s.ReturnWeeks(150, "A", aID, []int{g % 4})
			_ = s.RegisterExemption(150, "B", bID, g%4)
		}(g)
	}
	wg.Wait()

	// 不变式: 每个单元格已分配数不超过容量。
	used := map[Cell]int{}
	for _, sv := range s.Snapshot().Series {
		for _, w := range sv.Weeks {
			if w.Returned {
				continue
			}
			used[Cell{Week: w.Week, Slot: Slot{Weekday: sv.Weekday, Hour: sv.Hour}}]++
		}
	}
	for c, n := range used {
		if n > cfg.Capacity {
			t.Fatalf("单元格 %+v 已分配 %d 超过容量 %d", c, n, cfg.Capacity)
		}
	}
}
