package prepaid

import (
	"math"
	"testing"
)

func baseCfg() Config {
	return Config{
		InitialPriceMilli:  1000,
		WarnThreshold:      100,
		RestoreThreshold:   20,
		EmergencyAmount:    50,
		EmergencyThreshold: 10,
		ArrearsRatioNum:    1,
		ArrearsRatioDen:    2,
		ConfirmTimeout:     30,
		PeriodLength:       100000,
	}
}

func friendlyCfg() Config {
	cfg := baseCfg()
	cfg.FriendlyStartSec = 100
	cfg.FriendlyEndSec = 200
	return cfg
}

func newAcct(t *testing.T, cfg Config) *Account {
	t.Helper()
	a, err := NewAccount(cfg)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return a
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func fail(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("want error %v, got %v", code, err)
	}
}

func eventsOfKind(evs []Event, k EventKind) []Event {
	var out []Event
	for _, e := range evs {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func TestConfigValidation(t *testing.T) {
	cfg := baseCfg()
	cfg.WarnThreshold = 0
	if _, err := NewAccount(cfg); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("warn threshold 0: %v", err)
	}
	cfg = baseCfg()
	cfg.RestoreThreshold = -1
	if _, err := NewAccount(cfg); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("restore threshold -1: %v", err)
	}
	cfg = baseCfg()
	cfg.RestoreThreshold = 0
	if _, err := NewAccount(cfg); err != nil {
		t.Fatalf("restore threshold 0 should be legal: %v", err)
	}
}

// 余额恰等于预警阈值不预警；跌破记一次；未回到阈值之上不重复；
// 回到阈值之上后再次跌破重新预警。
func TestWarningThresholdBoundary(t *testing.T) {
	a := newAcct(t, baseCfg())
	ok(t, a.Recharge(1, 150))
	ok(t, a.AddReading(2, 0))
	ok(t, a.AddReading(10, 50)) // 扣 50，余额恰为 100 == 阈值，不预警
	if n := len(eventsOfKind(a.Events(), EvWarning)); n != 0 {
		t.Fatalf("balance == threshold should not warn, got %d warnings", n)
	}
	ok(t, a.AddReading(20, 51)) // 扣 1，余额 99 < 阈值，预警一次
	ok(t, a.AddReading(30, 60)) // 扣 9，余额 90，未回到阈值之上，不重复
	if n := len(eventsOfKind(a.Events(), EvWarning)); n != 1 {
		t.Fatalf("want 1 warning, got %d", n)
	}
	ok(t, a.Recharge(40, 50))    // 余额 140，闩锁复位
	ok(t, a.AddReading(50, 101)) // 扣 41，余额 99，再次跌破，重新预警
	if n := len(eventsOfKind(a.Events(), EvWarning)); n != 2 {
		t.Fatalf("want 2 warnings after re-arm, got %d", n)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}

// 拒绝次序固定：参数非法 > 时钟回退 > 时序错误 > 读数倒退 >
// 状态不允许 > 本周期已启用 > 未达启用条件 > 确认超时。
func TestRejectionOrder(t *testing.T) {
	a := newAcct(t, baseCfg())
	// 参数非法优先于时钟回退。
	fail(t, a.Recharge(-1, 0), ErrInvalidParam)
	ok(t, a.Recharge(1, 100))
	fail(t, a.Recharge(0, 0), ErrInvalidParam) // 金额非正 + 时刻回退，报参数非法
	fail(t, a.Recharge(0, 10), ErrClockRegression)
	// 时钟回退 > 时序错误 > 读数倒退。
	ok(t, a.AddReading(10, 5))
	fail(t, a.AddReading(5, 100), ErrClockRegression)
	fail(t, a.AddReading(10, 100), ErrTimeOrder)     // 时刻未严格更晚
	fail(t, a.AddReading(20, 3), ErrReadingRollback) // 电量减少
	// 确认超时是优先级最低的错误：状态不对时报状态不允许。
	fail(t, a.ConfirmRestore(1000), ErrStateNotAllowed)

	// 状态不允许 > 本周期已启用：已停电时启用应急，即使本周期已启用过。
	b := newAcct(t, baseCfg())
	ok(t, b.Recharge(1, 5))
	ok(t, b.EnableEmergency(2)) // 本周期已启用
	ok(t, b.AddReading(3, 0))
	ok(t, b.AddReading(10, 100)) // 余额 -45，立即停电
	fail(t, b.EnableEmergency(11), ErrStateNotAllowed)

	// 本周期已启用 > 未达启用条件：两者同时成立时报前者。
	c := newAcct(t, baseCfg())
	ok(t, c.Recharge(1, 5))
	ok(t, c.EnableEmergency(2)) // 余额 55，既已启用又不满足余额条件
	fail(t, c.EnableEmergency(3), ErrAlreadyEnabled)

	// 被拒绝的操作不改变余额、欠费、状态、事件与时钟。
	d := newAcct(t, baseCfg())
	ok(t, d.Recharge(1, 100))
	before := d.Snapshot()
	nEvents := len(d.Events())
	fail(t, d.Recharge(0, 10), ErrClockRegression)
	fail(t, d.AddReading(0, 5), ErrClockRegression) // 时刻早于当前时钟
	fail(t, d.EnableEmergency(2), ErrConditionNotMet)
	after := d.Snapshot()
	if before != after || len(d.Events()) != nEvents {
		t.Fatalf("rejected ops changed state: %+v -> %+v", before, after)
	}
}

// 已停电状态下登记的读数若电量增加，照常扣费并记「停电期间用电」事件。
func TestOffGridUsageStillCharged(t *testing.T) {
	a := newAcct(t, baseCfg())
	ok(t, a.Recharge(1, 5))
	ok(t, a.AddReading(2, 0))
	ok(t, a.AddReading(10, 10)) // 余额 -5，立即停电，欠费 5
	ok(t, a.AddReading(20, 18)) // 停电期间用电 8，照常扣费
	charges := eventsOfKind(a.Events(), EvCharge)
	offgrid := eventsOfKind(a.Events(), EvOffGridUsage)
	if len(offgrid) != 1 || offgrid[0].Energy != 8 {
		t.Fatalf("want off-grid usage of 8, got %v", offgrid)
	}
	if len(charges) != 2 || charges[1].Amount != 8 {
		t.Fatalf("want charge of 8 during cutoff, got %v", charges)
	}
	if s := a.Snapshot(); s.State != StateCutOff || s.Balance != -8 {
		t.Fatalf("want balance -8 in cutoff, got %+v", s)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}

// 电价变更在指定时刻生效，不影响已计费区间；
// 区间按起点时刻生效的电价计费。
func TestPriceChangeNotRetroactive(t *testing.T) {
	a := newAcct(t, baseCfg()) // 初始电价 1000（1 元/单位）
	ok(t, a.Recharge(1, 1000))
	ok(t, a.AddReading(10, 0))
	ok(t, a.SetPrice(50, 2000))
	fail(t, a.SetPrice(50, 3000), ErrTimeOrder) // 电价变更时刻须严格递增
	ok(t, a.AddReading(100, 10))                // 区间 [10,100) 起点电价 1000 → 扣 10
	ok(t, a.AddReading(200, 20))                // 区间 [100,200) 起点电价 2000 → 扣 20
	charges := eventsOfKind(a.Events(), EvCharge)
	if len(charges) != 2 || charges[0].Amount != 10 || charges[1].Amount != 20 {
		t.Fatalf("bad charges: %v", charges)
	}
	if charges[0].Price != 1000 || charges[1].Price != 2000 {
		t.Fatalf("bad prices: %v", charges)
	}
}

// 节假日与休息日全天友好；连续友好日顺延；全周休息则推迟无界。
func TestHolidayAndRestDayDeferral(t *testing.T) {
	cfg := baseCfg()
	cfg.RestWeekdays = [7]bool{6: true} // 每周第 6 天休息
	a := newAcct(t, cfg)
	ok(t, a.Recharge(1, 10))
	ok(t, a.AddReading(2, 0))
	ok(t, a.AddReading(6*secondsPerDay+5000, 20)) // 休息日内余额转负
	if s := a.Snapshot(); s.CutoffExecAt != 7*secondsPerDay {
		t.Fatalf("rest day deferral: want exec at day 7 start, got %d", s.CutoffExecAt)
	}

	b := newAcct(t, cfg)
	ok(t, b.AddHoliday(10))
	ok(t, b.AddHoliday(11)) // 连续两个节假日
	ok(t, b.Recharge(1, 10))
	ok(t, b.AddReading(2, 0))
	ok(t, b.AddReading(10*secondsPerDay+100, 20))
	if s := b.Snapshot(); s.CutoffExecAt != 12*secondsPerDay {
		t.Fatalf("consecutive holidays: want exec at day 12 start, got %d", s.CutoffExecAt)
	}

	allRest := baseCfg()
	allRest.RestWeekdays = [7]bool{true, true, true, true, true, true, true}
	c := newAcct(t, allRest)
	ok(t, c.Recharge(1, 10))
	ok(t, c.AddReading(2, 0))
	ok(t, c.AddReading(100, 20))
	if s := c.Snapshot(); s.CutoffExecAt != math.MaxInt64 {
		t.Fatalf("all-rest deferral should be unbounded, got %d", s.CutoffExecAt)
	}
}

// 搭建一个处于待复电状态的账户：欠费 5，余额 35，确认截止时刻 50。
func setupPendingRestore(t *testing.T, cfg Config) *Account {
	t.Helper()
	a := newAcct(t, cfg)
	ok(t, a.Recharge(1, 5))
	ok(t, a.AddReading(2, 0))
	ok(t, a.AddReading(10, 10)) // 扣 10，余额 -5，立即停电：欠费 5
	ok(t, a.Recharge(20, 40))   // 清偿欠费 5（floor(40/2)=20 封顶为 5），35 入余额
	if s := a.Snapshot(); s.State != StatePendingRestore || s.RestoreDeadline != 50 {
		t.Fatalf("want pending restore with deadline 50, got %+v", s)
	}
	return a
}

// 复电确认恰在时限到期时刻成功；超过时限报「确认超时」；
// 时钟越过时限回退已停电；再次达到条件须再次进入待复电。
func TestRestoreConfirmDeadline(t *testing.T) {
	a := setupPendingRestore(t, baseCfg())
	ok(t, a.ConfirmRestore(50)) // 恰在到期时刻，成功
	if s := a.Snapshot(); s.State != StateSupplyOn {
		t.Fatalf("want supply on, got %v", s.State)
	}

	b := setupPendingRestore(t, baseCfg())
	fail(t, b.ConfirmRestore(51), ErrConfirmTimeout) // 超时确认被拒绝，状态不变
	if s := b.Snapshot(); s.State != StatePendingRestore || s.Clock != 20 {
		t.Fatalf("rejected confirm must not change state, got %+v", s)
	}
	ok(t, b.AdvanceClock(51)) // 时钟越过时限，回退已停电
	if s := b.Snapshot(); s.State != StateCutOff {
		t.Fatalf("want cutoff after expiry, got %v", s.State)
	}
	fail(t, b.ConfirmRestore(52), ErrStateNotAllowed)
	ok(t, b.Recharge(60, 10)) // 余额 45 再次达到复电阈值，重新进入待复电
	if s := b.Snapshot(); s.State != StatePendingRestore || s.RestoreDeadline != 90 {
		t.Fatalf("want re-entered pending restore, got %+v", s)
	}
	ok(t, b.ConfirmRestore(90))
	if s := b.Snapshot(); s.State != StateSupplyOn {
		t.Fatalf("want supply on, got %v", s.State)
	}
	if !b.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}

// 待复电期间余额再跌破复电阈值：取消待复电，回到已停电。
func TestPendingRestoreCancelledOnBalanceDrop(t *testing.T) {
	a := setupPendingRestore(t, baseCfg())
	ok(t, a.AddReading(30, 30)) // 扣 20，余额 15 < 复电阈值 20
	if s := a.Snapshot(); s.State != StateCutOff {
		t.Fatalf("want cutoff after balance drop, got %v", s.State)
	}
	if n := len(eventsOfKind(a.Events(), EvRestoreCancelled)); n != 1 {
		t.Fatalf("want 1 restore-cancelled event, got %d", n)
	}
}

// 跨周期的停电时长按边界切分计入各自周期。
func TestCrossPeriodCutoffDuration(t *testing.T) {
	cfg := baseCfg()
	cfg.PeriodLength = 100
	a := newAcct(t, cfg)
	ok(t, a.Recharge(1, 5))
	ok(t, a.AddReading(2, 0))
	ok(t, a.AddReading(50, 50)) // 扣 50，余额 -45，时刻 50 立即停电
	ok(t, a.AdvanceClock(250))  // 跨越边界 100、200
	sums := eventsOfKind(a.Events(), EvPeriodSummary)
	if len(sums) != 2 {
		t.Fatalf("want 2 summaries, got %d", len(sums))
	}
	if sums[0].Period != 0 || sums[0].CutoffDuration != 50 { // [50,100)
		t.Fatalf("period 0 cutoff duration: %+v", sums[0])
	}
	if sums[1].Period != 1 || sums[1].CutoffDuration != 100 { // [100,200)
		t.Fatalf("period 1 cutoff duration: %+v", sums[1])
	}
	ok(t, a.Recharge(260, 100)) // 清偿欠费 45，55 入余额，进入待复电
	ok(t, a.ConfirmRestore(270))
	ok(t, a.AdvanceClock(301))
	sums = eventsOfKind(a.Events(), EvPeriodSummary)
	if len(sums) != 3 || sums[2].Period != 2 || sums[2].CutoffDuration != 60 { // [200,260)
		t.Fatalf("period 2 cutoff duration: %+v", sums)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}

// 阈值变更本身不触发预警；其后的余额变化按新阈值判定。
func TestThresholdChangeDoesNotWarn(t *testing.T) {
	a := newAcct(t, baseCfg())
	ok(t, a.Recharge(1, 150))
	ok(t, a.AddReading(2, 0))
	fail(t, a.SetWarnThreshold(0), ErrInvalidParam)
	ok(t, a.SetWarnThreshold(200)) // 余额 150 < 200，但变更本身不预警
	if n := len(eventsOfKind(a.Events(), EvWarning)); n != 0 {
		t.Fatalf("threshold change must not warn, got %d", n)
	}
	ok(t, a.AddReading(10, 60)) // 扣 60，余额 90 < 200，余额变化触发预警
	if n := len(eventsOfKind(a.Events(), EvWarning)); n != 1 {
		t.Fatalf("want 1 warning after balance drop, got %d", n)
	}
}

// 恰在友好时段起点触发停电：推迟到时段结束；
// 恰在终点触发：时段左闭右开，终点不友好，立即执行。
func TestCutoffAtFriendlyWindowEdges(t *testing.T) {
	// 起点 t=100（友好时段 [100,200) 内）→ 推迟到 200。
	a := newAcct(t, friendlyCfg())
	ok(t, a.Recharge(1, 10))
	ok(t, a.AddReading(2, 0))
	ok(t, a.AddReading(100, 20)) // 扣 20，余额 -10
	snap := a.Snapshot()
	if snap.State != StatePendingCutoff || snap.CutoffExecAt != 200 {
		t.Fatalf("want pending cutoff at 200, got state=%v exec=%d", snap.State, snap.CutoffExecAt)
	}
	ok(t, a.AdvanceClock(199))
	if s := a.Snapshot(); s.State != StatePendingCutoff {
		t.Fatalf("at 199 should still be pending, got %v", s.State)
	}
	ok(t, a.AdvanceClock(200))
	if s := a.Snapshot(); s.State != StateCutOff || s.Balance != 0 || s.Arrears != 10 {
		t.Fatalf("at 200 want cutoff with arrears 10, got %+v", s)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}

	// 终点 t=200（不友好）→ 执行时刻即当前时刻，立即转已停电。
	b := newAcct(t, friendlyCfg())
	ok(t, b.Recharge(1, 10))
	ok(t, b.AddReading(2, 0))
	ok(t, b.AddReading(200, 20))
	if s := b.Snapshot(); s.State != StateCutOff || s.Arrears != 10 || s.Balance != 0 {
		t.Fatalf("at window end want immediate cutoff, got %+v", s)
	}
	execs := eventsOfKind(b.Events(), EvCutoffExecuted)
	if len(execs) != 1 || execs[0].Time != 200 {
		t.Fatalf("want cutoff executed at 200, got %v", execs)
	}
}

// 待停电期间充值使余额回到非负：取消停电、回到送电。
func TestRechargeCancelsPendingCutoff(t *testing.T) {
	a := newAcct(t, friendlyCfg())
	ok(t, a.Recharge(1, 10))
	ok(t, a.AddReading(2, 0))
	ok(t, a.AddReading(100, 20)) // 余额 -10，待停电，执行时刻 200
	ok(t, a.Recharge(150, 20))   // 余额 10，取消待停电
	if s := a.Snapshot(); s.State != StateSupplyOn {
		t.Fatalf("want supply on after recharge, got %v", s.State)
	}
	if n := len(eventsOfKind(a.Events(), EvCutoffCancelled)); n != 1 {
		t.Fatalf("want 1 cancel event, got %d", n)
	}
	ok(t, a.AdvanceClock(300)) // 原执行时刻已过，无事发生
	if s := a.Snapshot(); s.State != StateSupplyOn {
		t.Fatalf("cutoff must stay cancelled, got %v", s.State)
	}
	if n := len(eventsOfKind(a.Events(), EvCutoffExecuted)); n != 0 {
		t.Fatalf("no cutoff should execute, got %d", n)
	}
}

// 应急启用恰在阈值：余额等于阈值不可启用，低于阈值可启用。
func TestEmergencyExactlyAtThreshold(t *testing.T) {
	a := newAcct(t, baseCfg())
	ok(t, a.Recharge(1, 10)) // 余额恰等于启用阈值 10
	fail(t, a.EnableEmergency(2), ErrConditionNotMet)
	ok(t, a.AddReading(3, 0))
	ok(t, a.AddReading(4, 1)) // 扣 1，余额 9 < 10
	ok(t, a.EnableEmergency(5))
	if s := a.Snapshot(); s.Balance != 59 || s.EmergencyUsed != 50 {
		t.Fatalf("want balance 59, emergency used 50, got %+v", s)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}

// 周期边界重置启用资格，但未偿还的应急额度不清零。
func TestPeriodBoundaryResetsEmergencyEligibility(t *testing.T) {
	cfg := baseCfg()
	cfg.PeriodLength = 100
	a := newAcct(t, cfg)
	ok(t, a.Recharge(1, 5))
	ok(t, a.EnableEmergency(2))
	fail(t, a.EnableEmergency(3), ErrAlreadyEnabled)
	ok(t, a.AddReading(4, 0))
	ok(t, a.AddReading(50, 50)) // 扣 50，余额 5，重新满足启用条件
	ok(t, a.AdvanceClock(101))  // 跨越周期边界 100
	ok(t, a.EnableEmergency(102))
	if s := a.Snapshot(); s.EmergencyUsed != 100 {
		t.Fatalf("emergency used must carry across periods, got %d", s.EmergencyUsed)
	}
	ok(t, a.Recharge(103, 120)) // 先偿还应急 100，余 20 按比例清偿欠费（为 0），全入余额
	evs := eventsOfKind(a.Events(), EvRecharge)
	last := evs[len(evs)-1]
	if last.RepayEmergency != 100 || last.RepayArrears != 0 || last.ToBalance != 20 {
		t.Fatalf("bad recharge split: %+v", last)
	}
	if s := a.Snapshot(); s.EmergencyUsed != 0 {
		t.Fatalf("emergency should be fully repaid, got %d", s.EmergencyUsed)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}

// 充值分配的三段顺序与比例取整：先还应急、再按比例（向下取整）清偿欠费、其余入余额。
func TestRechargeAllocationOrderAndFloor(t *testing.T) {
	a := newAcct(t, baseCfg()) // 比例 1/2
	ok(t, a.Recharge(1, 5))
	ok(t, a.EnableEmergency(2)) // 余额 55，应急已用 50
	ok(t, a.AddReading(3, 0))
	ok(t, a.AddReading(10, 140)) // 扣 140，余额 -85，立即停电：欠费 85
	if s := a.Snapshot(); s.State != StateCutOff || s.Arrears != 85 {
		t.Fatalf("want cutoff with arrears 85, got %+v", s)
	}
	ok(t, a.Recharge(20, 200)) // 还应急 50；余 150，一半 75 清偿欠费；其余 75 入余额
	evs := eventsOfKind(a.Events(), EvRecharge)
	last := evs[len(evs)-1]
	if last.RepayEmergency != 50 || last.RepayArrears != 75 || last.ToBalance != 75 {
		t.Fatalf("bad split: %+v", last)
	}
	if s := a.Snapshot(); s.Arrears != 10 || s.Balance != 75 {
		t.Fatalf("want arrears 10 balance 75, got %+v", s)
	}
	// 比例取整且以欠费为上限：余 9 的一半向下取整为 4，但欠费只剩 10。
	ok(t, a.Recharge(30, 9)) // 余 9，floor(9/2)=4 清偿欠费，5 入余额
	evs = eventsOfKind(a.Events(), EvRecharge)
	last = evs[len(evs)-1]
	if last.RepayArrears != 4 || last.ToBalance != 5 {
		t.Fatalf("bad floor split: %+v", last)
	}
	ok(t, a.Recharge(40, 100)) // 余 100，一半 50 但欠费仅 6，清偿 6，94 入余额
	evs = eventsOfKind(a.Events(), EvRecharge)
	last = evs[len(evs)-1]
	if last.RepayArrears != 6 || last.ToBalance != 94 {
		t.Fatalf("bad capped split: %+v", last)
	}
	if s := a.Snapshot(); s.Arrears != 0 {
		t.Fatalf("arrears should be cleared, got %d", s.Arrears)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}
