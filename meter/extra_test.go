package meter

import "testing"

// 应急启用恰在阈值：== 阈值不允许，< 阈值允许；每周期一次；已停电拒绝。
func TestEmergencyAtThreshold(t *testing.T) {
	cfg := baseCfg()
	cfg.EmergencyThreshold = 20
	cfg.Tariffs = []Tariff{{Start: 0, Price: 10}}
	c := New(cfg, 50)
	_, _ = c.AddReading(1, 0)
	_, _ = c.AddReading(10, 3000) // 扣 30，余额 20 == 阈值
	if _, err := c.EnableEmergency(11); err != ErrNotEligible {
		t.Fatalf("balance == threshold: %v", err)
	}
	_, _ = c.AddReading(20, 3100) // 余额 19
	evs, err := c.EnableEmergency(21)
	if err != nil {
		t.Fatalf("below threshold enables: %v", err)
	}
	if s := c.Snapshot(); s.Balance != 119 || s.EmergencyUsed != 100 {
		t.Fatalf("grant: %+v", s)
	}
	if !hasKind(evs, EvEmergencyGranted) {
		t.Fatal("missing grant")
	}
	if _, err := c.EnableEmergency(22); err != ErrAlreadyUsed {
		t.Fatalf("twice: %v", err)
	}

	c3 := New(cfg, 0)
	_, _ = c3.AddReading(0, 0)
	_, _ = c3.AddReading(1, 5000)
	if _, err := c3.EnableEmergency(2); err != ErrStateForbidden {
		t.Fatalf("cut forbids: %v", err)
	}
}

// 周期边界重置资格但不清零应急已用。
func TestCycleResetEmergency(t *testing.T) {
	cfg := baseCfg()
	cfg.EmergencyThreshold = 1000
	c := New(cfg, 0)
	_, _ = c.EnableEmergency(1)
	evs, _ := c.Advance(1000)
	if !hasKind(evs, EvCycleSummary) {
		t.Fatal("missing summary")
	}
	s := c.Snapshot()
	if s.EmergencyEnabledThisCycle {
		t.Fatal("eligibility must reset")
	}
	if s.EmergencyUsed != 100 {
		t.Fatalf("used must survive: %d", s.EmergencyUsed)
	}
	if _, err := c.EnableEmergency(1001); err != nil {
		t.Fatalf("enable again: %v", err)
	}
}

// 充值三段分配与比例取整。
func TestRechargeAllocation(t *testing.T) {
	cfg := baseCfg()
	cfg.DebtRepayRatio = Ratio{N: 2, D: 3}
	cfg.RestoreThreshold = 0
	cfg.Tariffs = []Tariff{{Start: 0, Price: 10}}
	cfg.FriendlyStart = 0
	cfg.FriendlyEnd = 86400
	cfg.CycleLength = 86400 * 10
	cfg.EmergencyAmount = 100
	cfg.EmergencyThreshold = 100
	c := New(cfg, 0)
	_, _ = c.EnableEmergency(1)
	_, _ = c.AddReading(50, 0)
	// 友好时段内：应急 100，扣 100 后余额 0，再扣 100 余额 -100，待停电。
	_, _ = c.AddReading(60, 10000)
	_, _ = c.AddReading(100, 20000)
	_, _ = c.Advance(86400) // 友好时段结束 -> 停电，负余额 100 转欠费
	if s := c.Snapshot(); s.Status != Cut || s.Debt != 100 {
		t.Fatalf("setup: %+v", s)
	}
	evs, err := c.Recharge(86500, 160)
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := findKind(evs, EvRecharge)
	if rc.EmergencyPaid != 100 || rc.DebtPaid != 40 || rc.ToBalance != 20 {
		t.Fatalf("allocation: %+v", rc)
	}
	if s := c.Snapshot(); s.EmergencyUsed != 0 || s.Debt != 60 || s.Balance != 20 {
		t.Fatalf("state: %+v", s)
	}
	evs, _ = c.Recharge(86600, 200)
	rc, _ = findKind(evs, EvRecharge)
	if rc.EmergencyPaid != 0 || rc.DebtPaid != 60 || rc.ToBalance != 140 {
		t.Fatalf("capped: %+v", rc)
	}
}

// 复电确认恰在时限到期时刻；超时拒绝并回到已停电；可再次进入待复电。
func TestRestoreConfirmDeadline(t *testing.T) {
	cfg := baseCfg()
	cfg.RestoreThreshold = 50
	cfg.Tariffs = []Tariff{{Start: 0, Price: 10}}
	c := New(cfg, 0)
	_, _ = c.AddReading(1, 0)
	_, _ = c.AddReading(10, 5000) // 扣 50，欠费 50
	evs, _ := c.Recharge(20, 100) // 应急0；欠费 100*1/2=50；50 入余额
	pr, _ := findKind(evs, EvPendingRestore)
	if c.Snapshot().Status != PendingRestore || pr.Deadline != 120 {
		t.Fatalf("offer: %+v", c.Snapshot())
	}
	evs, err := c.ConfirmRestore(120)
	if err != nil || c.Snapshot().Status != Powered || !hasKind(evs, EvRestored) {
		t.Fatalf("at deadline succeeds: %v %v", kinds(evs), err)
	}

	_, _ = c.AddReading(125, 0)
	_, _ = c.AddReading(130, 11000) // 区间扣 110，余额 -60 -> 停电，欠费 60
	evs, _ = c.Recharge(140, 1000)  // 还欠费 51，余额 949
	if c.Snapshot().Status != PendingRestore {
		t.Fatalf("offer2: %v", kinds(evs))
	}
	if _, err := c.ConfirmRestore(241); err != ErrConfirmTimeout {
		t.Fatalf("late: %v", err)
	}
	// 被拒绝操作不改变状态；推进时钟越过时限才发出超时事件、回到已停电。
	if c.Snapshot().Status != PendingRestore {
		t.Fatal("rejected confirmation must not mutate state")
	}
	evs, _ = c.Advance(241)
	if c.Snapshot().Status != Cut || !hasKind(evs, EvRestoreTimeout) {
		t.Fatalf("advance past deadline times out: %v", kinds(evs))
	}
	evs, _ = c.Recharge(300, 1000)
	if !hasKind(evs, EvPendingRestore) {
		t.Fatalf("re-offer: %v", kinds(evs))
	}
}

// 待复电期间余额再跌破：取消待复电，回到已停电。
func TestPendingRestoreCanceledOnDrop(t *testing.T) {
	cfg := baseCfg()
	cfg.RestoreThreshold = 50
	cfg.Tariffs = []Tariff{{Start: 0, Price: 10}}
	c := New(cfg, 0)
	_, _ = c.AddReading(1, 0)
	_, _ = c.AddReading(10, 5000) // 欠费 50
	evs, _ := c.Recharge(20, 1000)
	if c.Snapshot().Status != PendingRestore {
		t.Fatalf("offer: %v", kinds(evs))
	}
	// 待复电期间登记大额用电：扣费后余额为负 -> 取消待复电、回到已停电。
	_, _ = c.AddReading(25, 0)
	evs, _ = c.AddReading(30, 95100) // 扣 951，余额 -1
	if c.Snapshot().Status != Cut || !hasKind(evs, EvRestoreCanceled) {
		t.Fatalf("drop cancels offer: %v %+v", kinds(evs), c.Snapshot())
	}
}

// 跨周期停电时长按边界切分；停电期间用电照常扣费并记事件。
func TestCrossCycleCutDuration(t *testing.T) {
	cfg := baseCfg()
	cfg.CycleLength = 1000
	cfg.Tariffs = []Tariff{{Start: 0, Price: 10}}
	c := New(cfg, 0)
	_, _ = c.AddReading(0, 0)
	// t=1 扣 50 -> 立即停电（t=1..t=1000 共 999 tick 落在第 0 周期）。
	evs, _ := c.AddReading(1, 5000)
	if c.Snapshot().Status != Cut {
		t.Fatalf("setup cut: %v", kinds(evs))
	}
	evs, _ = c.Advance(900)
	if got := c.Snapshot().CycleCutDuration; got != 899 {
		t.Fatalf("within-cycle cut duration [1,900): %d", got)
	}
	// 停电期间电量增加：照常扣费 + 停电期间用电事件。
	evs, _ = c.AddReading(950, 6000) // 区间增量 1000 Wh，扣 10
	if !hasKind(evs, EvDuringCutUsage) {
		t.Fatalf("during-cut usage event: %v", kinds(evs))
	}
	evs, _ = c.Advance(1100)
	sum, ok := findKind(evs, EvCycleSummary)
	if !ok {
		t.Fatal("missing boundary summary")
	}
	// 第 0 周期停电 999 tick（t=1..t=1000）。
	if sum.Cycle != 0 || sum.CutDuration != 999 {
		t.Fatalf("cycle0 cut duration: %+v", sum)
	}
	if c.Snapshot().CycleIndex != 1 || c.Snapshot().CycleCutDuration != 100 {
		t.Fatalf("cycle1 cut duration: %+v", c.Snapshot())
	}
	// 总停电时长无遗漏：0..1 周期合计 999+100=1099 = 1100-1。
}

// 拒绝次序固定：参数非法 > 时钟回退 > 时序错误 > 读数倒退 >
// 状态不允许 > 本周期已启用 > 未达启用条件 > 确认超时。
func TestRejectionOrder(t *testing.T) {
	cfg := baseCfg()
	cfg.EmergencyThreshold = -1000 // 保证有资格
	c := New(cfg, 1000)

	// 参数非法优先于一切：负金额 + 过去时刻。
	if _, err := c.Recharge(0, -5); err != ErrInvalidParam {
		t.Fatalf("invalid param first: %v", err)
	}
	if _, err := c.AddReading(-1, 0); err != ErrInvalidParam {
		t.Fatalf("time before creation: %v", err)
	}
	// 时钟回退优先于读数错误。
	_, _ = c.AddReading(100, 10)
	if _, err := c.AddReading(50, 20); err != ErrClockBack {
		t.Fatalf("clockback: %v", err)
	}
	// 时序错误优先于读数倒退。
	if _, err := c.AddReading(100, 9); err != ErrOrder {
		t.Fatalf("order before regression: %v", err)
	}
	if _, err := c.AddReading(101, 9); err != ErrReadingBack {
		t.Fatalf("regression: %v", err)
	}

	// 应急：已停电 -> 状态不允许（优先于周期已启用等）。
	c2 := New(cfg, 0)
	_, _ = c2.AddReading(0, 0)
	_, _ = c2.AddReading(1, 5000) // 停电
	if _, err := c2.EnableEmergency(2); err != ErrStateForbidden {
		t.Fatalf("state before used: %v", err)
	}
	// 送电、本周期已启用优先于未达条件。
	c3cfg := baseCfg()
	c3cfg.EmergencyThreshold = 100000
	c3 := New(c3cfg, 1000)
	_, _ = c3.EnableEmergency(1)
	if _, err := c3.EnableEmergency(2); err != ErrAlreadyUsed {
		t.Fatalf("already used: %v", err)
	}
	// 未达条件。
	if _, err := New(baseCfg(), 1000).EnableEmergency(1); err != ErrNotEligible {
		t.Fatalf("not eligible: %v", err)
	}

	// 确认超时：非待复电 -> 状态不允许；待复电过期 -> 确认超时。
	if _, err := c.ConfirmRestore(200); err != ErrStateForbidden {
		t.Fatalf("confirm state: %v", err)
	}
}

// 参数约束：复电阈值 < 0、预警阈值 <= 0 非法。
func TestInvalidParams(t *testing.T) {
	cfg := baseCfg()
	cfg.RestoreThreshold = -1
	if c := New(cfg, 0); c != nil {
		t.Fatal("negative restore threshold invalid")
	}
	cfg = baseCfg()
	cfg.WarnThreshold = 0
	if c := New(cfg, 0); c != nil {
		t.Fatal("zero warn threshold invalid")
	}
	c := New(baseCfg(), 0)
	if err := c.SetWarnThreshold(0); err != ErrInvalidParam {
		t.Fatalf("set warn: %v", err)
	}
	if err := c.AddTariff(0, -1); err != ErrInvalidParam {
		t.Fatalf("negative price: %v", err)
	}
}
