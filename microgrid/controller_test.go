package microgrid

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func baseConfig() Config {
	return Config{
		Capacity:             100,
		MinSoC:               10,
		MaxSoC:               90,
		MaxChargePerSlot:     40,
		MaxDischargePerSlot:  40,
		ChargeLossPermille:   0,
		MaintenanceThreshold: 10000,
		ReserveHorizon:       0,
		DeviationTolerance:   5,
		InitialSoC:           50,
	}
}

// newWithForecast 构造控制器并为时隙 1..slots 登记零值预测。
func newWithForecast(t *testing.T, cfg Config, slots int) *Controller {
	t.Helper()
	c, err := NewController(cfg)
	if err != nil {
		t.Fatalf("构造控制器失败: %v", err)
	}
	if slots > 0 {
		if _, err := c.UpdateForecast(1, make([]int, slots)); err != nil {
			t.Fatalf("登记预测失败: %v", err)
		}
	}
	return c
}

// advance 以闲置实际值推进 n 个时隙。
func advance(t *testing.T, c *Controller, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		cur := c.Snapshot().Current
		if _, err := c.RecordActual(cur, ActionIdle, 0); err != nil {
			t.Fatalf("推进时隙 %d 失败: %v", cur, err)
		}
	}
}

func reject(t *testing.T, err error) *RejectError {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("期望 RejectError，得到 %v", err)
	}
	return re
}

func mustReject(t *testing.T, err error, kind ErrKind, slot int) {
	t.Helper()
	re := reject(t, err)
	if re.Kind != kind || re.Slot != slot {
		t.Fatalf("期望 (%v, 时隙%d)，得到 (%v, 时隙%d)", kind, slot, re.Kind, re.Slot)
	}
}

func plan(actions ...PlanAction) []PlanAction { return actions }

func dis(amount int) PlanAction { return PlanAction{ActionDischarge, amount} }
func chg(amount int) PlanAction { return PlanAction{ActionCharge, amount} }
func idle() PlanAction          { return PlanAction{ActionIdle, 0} }
func repeat(a PlanAction, n int) []PlanAction {
	acts := make([]PlanAction, n)
	for i := range acts {
		acts[i] = a
	}
	return acts
}

// 荷电恰在上限/下限：取等允许，越过则报越界。
func TestSoCExactBounds(t *testing.T) {
	cfg := baseConfig()
	c := newWithForecast(t, cfg, 5)
	if err := c.SubmitPlan(1, plan(chg(40))); err != nil { // 50+40=90，恰在上限
		t.Fatalf("恰到上限应被接受: %v", err)
	}
	advance(t, c, 1)
	if _, err := c.RecordActual(1, ActionCharge, 40); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if got := c.Snapshot().SoC; got != 90 {
		t.Fatalf("荷电应为 90，得到 %d", got)
	}
	mustReject(t, c.SubmitPlan(3, plan(chg(1))), ErrOutOfBounds, 3)

	c2 := newWithForecast(t, cfg, 5)
	if err := c2.SubmitPlan(1, plan(dis(40))); err != nil { // 50-40=10，恰在下限
		t.Fatalf("恰到下限应被接受: %v", err)
	}
	advance(t, c2, 1)
	if _, err := c2.RecordActual(1, ActionDischarge, 40); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if got := c2.Snapshot().SoC; got != 10 {
		t.Fatalf("荷电应为 10，得到 %d", got)
	}
	mustReject(t, c2.SubmitPlan(3, plan(dis(1))), ErrOutOfBounds, 3)
}

// 部分重叠提交：未覆盖的旧时隙保留，但保留部分重新推演不成立则整次拒绝，
// 且拒绝后旧计划保持不变。
func TestPartialOverlapKeepsOldOnReject(t *testing.T) {
	cfg := baseConfig()
	c := newWithForecast(t, cfg, 10)
	// 旧计划：时隙3..6 各放 10，推演 50→40→30→20→10，恰在下限。
	if err := c.SubmitPlan(3, repeat(dis(10), 4)); err != nil {
		t.Fatalf("旧计划应被接受: %v", err)
	}
	// 新计划覆盖时隙1..4，保留的时隙5..6 推演至 0，越界 → 整次拒绝。
	mustReject(t, c.SubmitPlan(1, repeat(dis(10), 4)), ErrOutOfBounds, 5)
	snap := c.Snapshot()
	want := map[int]PlanAction{3: dis(10), 4: dis(10), 5: dis(10), 6: dis(10)}
	if !reflect.DeepEqual(snap.Accepted, want) {
		t.Fatalf("拒绝后旧计划应保持不变，得到 %v", snap.Accepted)
	}
	if snap.SoC != 50 {
		t.Fatalf("拒绝后荷电应不变，得到 %d", snap.SoC)
	}
	// 兼容的重叠提交：覆盖时隙5..6 为充电，保留的时隙3..4 仍成立。
	if err := c.SubmitPlan(5, repeat(chg(5), 2)); err != nil {
		t.Fatalf("兼容的重叠提交应被接受: %v", err)
	}
	want = map[int]PlanAction{3: dis(10), 4: dis(10), 5: chg(5), 6: chg(5)}
	if got := c.Snapshot().Accepted; !reflect.DeepEqual(got, want) {
		t.Fatalf("重叠替换结果不符，得到 %v", got)
	}
}

// 预测更新只撤销自首个不满足时隙起的后缀，之前的保留。
func TestForecastUpdateRevokesSuffixOnly(t *testing.T) {
	cfg := baseConfig()
	cfg.MinSoC = 0
	cfg.ReserveHorizon = 1
	c := newWithForecast(t, cfg, 6)
	if err := c.SubmitPlan(1, repeat(dis(5), 5)); err != nil {
		t.Fatalf("计划应被接受: %v", err)
	}
	// 时隙4 预测改为 100：时隙3 末可放电量 35 < 100，自时隙3 起撤销。
	revoked, err := c.UpdateForecast(4, []int{100})
	if err != nil {
		t.Fatalf("预测更新不会被拒绝: %v", err)
	}
	if !reflect.DeepEqual(revoked, []int{3, 4, 5}) {
		t.Fatalf("应只撤销后缀 [3 4 5]，得到 %v", revoked)
	}
	want := map[int]PlanAction{1: dis(5), 2: dis(5)}
	if got := c.Snapshot().Accepted; !reflect.DeepEqual(got, want) {
		t.Fatalf("时隙1..2 应保留，得到 %v", got)
	}
	for i, r := range c.Revocations() {
		if r.Seq != i || r.Reason != ReasonForecastUpdate || r.Slot != i+3 {
			t.Fatalf("撤销记录不符: %+v", r)
		}
	}
}

// 偏差恰等于容忍量不触发重推演，超过才触发。
func TestDeviationToleranceBoundary(t *testing.T) {
	cfg := baseConfig()
	cfg.DeviationTolerance = 5
	build := func(t *testing.T) *Controller {
		c := newWithForecast(t, cfg, 5)
		if err := c.SubmitPlan(1, []PlanAction{dis(10), dis(30)}); err != nil {
			t.Fatalf("计划应被接受: %v", err)
		}
		advance(t, c, 1)
		return c
	}
	// 恰等于容忍量：|15-10|=5，不触发。
	c1 := build(t)
	revoked, err := c1.RecordActual(1, ActionDischarge, 15)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("偏差恰等于容忍量不应撤销，得到 %v", revoked)
	}
	if _, ok := c1.Snapshot().Accepted[2]; !ok {
		t.Fatal("时隙2 计划应保留")
	}
	// 超过容忍量：|16-10|=6>5，按新荷电 34 重推演，时隙2 放 30 → 4<10 越界被撤销。
	c2 := build(t)
	revoked, err = c2.RecordActual(1, ActionDischarge, 16)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !reflect.DeepEqual(revoked, []int{2}) {
		t.Fatalf("应撤销 [2]，得到 %v", revoked)
	}
}

// 孤岛模式：放电量恰等于关键负荷预测时允许，超过则报模式不允许；充电受盈余约束。
func TestIslandModeLimits(t *testing.T) {
	cfg := baseConfig()
	newIsland := func(t *testing.T) *Controller {
		c := newWithForecast(t, cfg, 4)
		if _, err := c.UpdateForecast(1, []int{8}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.SetMode(ModeIsland); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c1 := newIsland(t)
	mustReject(t, c1.SubmitPlan(1, plan(dis(9))), ErrModeNotAllowed, 1)
	if err := c1.SubmitPlan(1, plan(dis(8))); err != nil { // 恰等于关键负荷
		t.Fatalf("孤岛放电恰等于关键负荷应被接受: %v", err)
	}
	c2 := newIsland(t)
	if _, err := c2.RegisterSurplus(1, 5); err != nil {
		t.Fatal(err)
	}
	mustReject(t, c2.SubmitPlan(1, plan(chg(6))), ErrModeNotAllowed, 1)
	if err := c2.SubmitPlan(1, plan(chg(5))); err != nil { // 恰等于盈余
		t.Fatalf("孤岛充电恰等于盈余应被接受: %v", err)
	}
	// 切回并网不引起撤销。
	revoked, err := c1.SetMode(ModeGrid)
	if err != nil || len(revoked) != 0 {
		t.Fatalf("切回并网不应撤销: revoked=%v err=%v", revoked, err)
	}
}

// 维护锁定阈值取等即达到；锁定期间拒绝含放电的新计划；维护完成后清零解锁。
func TestMaintenanceThresholdExact(t *testing.T) {
	cfg := baseConfig()
	cfg.MaintenanceThreshold = 30
	c := newWithForecast(t, cfg, 5)
	if err := c.SubmitPlan(1, plan(dis(30))); err != nil {
		t.Fatalf("计划应被接受: %v", err)
	}
	advance(t, c, 1)
	if _, err := c.RecordActual(1, ActionDischarge, 29); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().Locked {
		t.Fatal("吞吐 29 < 30，不应锁定")
	}
	if _, err := c.RecordActual(2, ActionDischarge, 1); err != nil { // 累计恰达 30
		t.Fatal(err)
	}
	if !c.Snapshot().Locked {
		t.Fatal("吞吐取等达到阈值，应进入维护锁定")
	}
	mustReject(t, c.SubmitPlan(4, plan(dis(1))), ErrMaintenanceLock, 4)
	c.CompleteMaintenance()
	snap := c.Snapshot()
	if snap.Locked || snap.Throughput != 0 {
		t.Fatalf("维护完成后应解锁且吞吐清零: %+v", snap)
	}
	if err := c.SubmitPlan(4, plan(dis(1))); err != nil {
		t.Fatalf("解锁后放电计划应被接受: %v", err)
	}
}

// 锁定触发时撤销未豁免的已接受放电时隙；孤岛模式下不超过关键负荷预测的放电是唯一例外。
func TestLockRevokesAndIslandException(t *testing.T) {
	cfg := baseConfig()
	cfg.MaintenanceThreshold = 10
	c := newWithForecast(t, cfg, 6)
	if err := c.SubmitPlan(1, repeat(dis(5), 3)); err != nil {
		t.Fatalf("计划应被接受: %v", err)
	}
	advance(t, c, 1)
	if _, err := c.RecordActual(1, ActionDischarge, 5); err != nil {
		t.Fatal(err)
	}
	revoked, err := c.RecordActual(2, ActionDischarge, 5) // 累计 10，取等锁定
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(revoked, []int{3}) {
		t.Fatalf("并网锁定应撤销放电时隙 [3]，得到 %v", revoked)
	}
	if !c.Snapshot().Locked {
		t.Fatal("应处于维护锁定")
	}
	// 并网模式下不放行任何放电。
	mustReject(t, c.SubmitPlan(4, plan(dis(1))), ErrMaintenanceLock, 4)
	// 孤岛例外：放电不超过关键负荷预测即可。
	if _, err := c.SetMode(ModeIsland); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateForecast(4, []int{5}); err != nil {
		t.Fatal(err)
	}
	if err := c.SubmitPlan(4, plan(dis(5))); err != nil {
		t.Fatalf("锁定期间孤岛豁免放电应被接受: %v", err)
	}
	// 超过关键负荷预测的放电不豁免；多类不满足时锁定优先于模式。
	mustReject(t, c.SubmitPlan(5, plan(dis(1))), ErrMaintenanceLock, 5)
}

// 拒绝次序固定，且报告首个不满足的时隙。
func TestRejectPrecedenceAndFirstSlot(t *testing.T) {
	// 越界优先于备用不足。
	cfg := baseConfig()
	cfg.MinSoC = 0
	cfg.MaxDischargePerSlot = 60
	cfg.ReserveHorizon = 1
	c := newWithForecast(t, cfg, 4)
	if _, err := c.UpdateForecast(2, []int{100}); err != nil {
		t.Fatal(err)
	}
	mustReject(t, c.SubmitPlan(1, plan(dis(60))), ErrOutOfBounds, 1)

	// 首个不满足的时隙优先：时隙2、3 均越界，报告时隙2。
	cfg2 := baseConfig()
	cfg2.MaxDischargePerSlot = 60
	c2 := newWithForecast(t, cfg2, 5)
	mustReject(t, c2.SubmitPlan(1, []PlanAction{idle(), dis(60), dis(60)}), ErrOutOfBounds, 2)

	// 模式不允许优先于越界。
	cfg3 := baseConfig()
	cfg3.MinSoC = 0
	cfg3.InitialSoC = 3
	c3 := newWithForecast(t, cfg3, 3)
	if _, err := c3.SetMode(ModeIsland); err != nil {
		t.Fatal(err)
	}
	mustReject(t, c3.SubmitPlan(1, plan(dis(5))), ErrModeNotAllowed, 1)

	// 参数非法优先于一切，且不改变任何状态。
	mustReject(t, c3.SubmitPlan(1, plan(dis(-1))), ErrInvalidParam, 1)
	mustReject(t, c3.SubmitPlan(1, nil), ErrInvalidParam, 1)
	if got := len(c3.Snapshot().Accepted); got != 0 {
		t.Fatalf("非法提交不应留下计划，得到 %d 个", got)
	}
}

// 执行登记：时隙错误与越界；被拒绝的登记不推进时隙。
func TestRecordActualErrors(t *testing.T) {
	cfg := baseConfig()
	c := newWithForecast(t, cfg, 3)
	var err error
	_, err = c.RecordActual(1, ActionIdle, 0)
	mustReject(t, err, ErrSlotMismatch, 1)
	_, err = c.RecordActual(-1, ActionIdle, 0)
	mustReject(t, err, ErrInvalidParam, -1)
	_, err = c.RecordActual(0, ActionIdle, 5)
	mustReject(t, err, ErrInvalidParam, 0)
	_, err = c.RecordActual(0, ActionDischarge, 100)
	mustReject(t, err, ErrOutOfBounds, 0)
	snap := c.Snapshot()
	if snap.Current != 0 || snap.SoC != 50 {
		t.Fatalf("被拒绝的登记不应推进时隙或改变荷电: %+v", snap)
	}
	if _, err := c.RecordActual(0, ActionIdle, 0); err != nil {
		t.Fatalf("合法登记应成功: %v", err)
	}
	if got := c.Snapshot().Current; got != 1 {
		t.Fatalf("时隙应推进到 1，得到 %d", got)
	}
}

// 相同操作序列重放得到完全相同的荷电轨迹与撤销记录。
func TestDeterministicReplay(t *testing.T) {
	cfg := baseConfig()
	cfg.ReserveHorizon = 2
	cfg.DeviationTolerance = 2
	cfg.MaintenanceThreshold = 60
	run := func() ([]int, []Revocation, Snapshot) {
		c, err := NewController(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var socTrace []int
		record := func() { socTrace = append(socTrace, c.Snapshot().SoC) }
		c.UpdateForecast(1, []int{3, 1, 4, 1, 5, 9, 2, 6})
		c.SubmitPlan(1, []PlanAction{dis(10), chg(5), dis(8), dis(2)})
		c.RecordActual(0, ActionIdle, 0)
		record()
		c.RecordActual(1, ActionDischarge, 12) // 偏差 2，恰等于容忍量
		record()
		c.SetMode(ModeIsland)
		c.RegisterSurplus(3, 20)
		c.UpdateForecast(4, []int{9, 9})
		c.RecordActual(2, ActionCharge, 5)
		record()
		c.SetMode(ModeGrid)
		c.CompleteMaintenance()
		c.RecordActual(3, ActionDischarge, 8)
		record()
		return socTrace, c.Revocations(), c.Snapshot()
	}
	trace1, rev1, snap1 := run()
	trace2, rev2, snap2 := run()
	if !reflect.DeepEqual(trace1, trace2) {
		t.Fatalf("荷电轨迹不一致: %v vs %v", trace1, trace2)
	}
	if !reflect.DeepEqual(rev1, rev2) {
		t.Fatalf("撤销记录不一致: %v vs %v", rev1, rev2)
	}
	if !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("最终状态不一致: %+v vs %+v", snap1, snap2)
	}
}

// 性能可验证性：登记实际值的推演步数只与此后已接受时隙数相关，与历史已执行时隙数无关。
func TestRecordActualCostIndependentOfHistory(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 100000
	cfg.MaxSoC = 100000
	cfg.MinSoC = 0
	cfg.InitialSoC = 50000
	cfg.MaxDischargePerSlot = 10
	cfg.DeviationTolerance = 0
	// c1 先执行 200 个历史时隙，c2 无历史；两者此后保持 9 个已接受时隙。
	c1 := newWithForecast(t, cfg, 0)
	advance(t, c1, 200)
	if _, err := c1.UpdateForecast(201, make([]int, 10)); err != nil {
		t.Fatal(err)
	}
	if err := c1.SubmitPlan(201, repeat(dis(1), 10)); err != nil {
		t.Fatal(err)
	}
	advance(t, c1, 1) // 执行时隙 200
	steps0 := c1.Snapshot().ReplaySteps
	if _, err := c1.RecordActual(201, ActionDischarge, 2); err != nil { // 偏差 1 > 0，触发重推演
		t.Fatal(err)
	}
	delta1 := c1.Snapshot().ReplaySteps - steps0

	c2 := newWithForecast(t, cfg, 10)
	if err := c2.SubmitPlan(1, repeat(dis(1), 10)); err != nil {
		t.Fatal(err)
	}
	advance(t, c2, 1)
	steps0 = c2.Snapshot().ReplaySteps
	if _, err := c2.RecordActual(1, ActionDischarge, 2); err != nil {
		t.Fatal(err)
	}
	delta2 := c2.Snapshot().ReplaySteps - steps0

	if delta1 != 9 || delta2 != 9 {
		t.Fatalf("重推演步数应等于剩余已接受时隙数 9，得到 %d 与 %d", delta1, delta2)
	}
}

// 性能可验证性：单时隙备用判定的预测访问次数等于备用时隙数，与预测总长度无关。
func TestReserveCheckCostIndependentOfForecastSize(t *testing.T) {
	cfg := baseConfig()
	cfg.ReserveHorizon = 3
	c, err := NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateForecast(1, make([]int, 10000)); err != nil { // 预测总长 10000
		t.Fatal(err)
	}
	if err := c.SubmitPlan(1, repeat(dis(1), 4)); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot().ForecastQueries; got != 3*4 {
		t.Fatalf("备用判定访问次数应为 备用时隙数×推演时隙数=12，得到 %d", got)
	}
}

// 并发冒烟：混合操作并发调用不panic、无数据竞争（配合 -race），状态始终可读。
func TestConcurrentSmoke(t *testing.T) {
	cfg := baseConfig()
	c := newWithForecast(t, cfg, 0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				cur := c.Snapshot().Current
				switch (g + i) % 5 {
				case 0:
					c.UpdateForecast(cur+1, []int{1, 2})
				case 1:
					c.SubmitPlan(cur+1, plan(dis(1)))
				case 2:
					c.RecordActual(cur, ActionIdle, 0)
				case 3:
					c.SetMode(Mode(g % 2))
				case 4:
					c.RegisterSurplus(cur+1, 2)
				}
			}
		}(g)
	}
	wg.Wait()
	snap := c.Snapshot()
	if snap.SoC < cfg.MinSoC || snap.SoC > cfg.MaxSoC {
		t.Fatalf("并发后荷电越界: %d", snap.SoC)
	}
}

// 备用恰好够：可放电量等于后续时隙关键负荷之和时接受，少 1 则拒绝。
func TestReserveExactlyEnough(t *testing.T) {
	cfg := baseConfig()
	cfg.ReserveHorizon = 2
	c := newWithForecast(t, cfg, 6)
	if _, err := c.UpdateForecast(2, []int{6, 4}); err != nil {
		t.Fatal(err)
	}
	// 时隙1末荷电 20，可放电量 20-10=10，恰等于 6+4。
	mustReject(t, c.SubmitPlan(1, plan(dis(31))), ErrReserveShortfall, 1)
	if err := c.SubmitPlan(1, plan(dis(30))); err != nil {
		t.Fatalf("备用恰好够应被接受: %v", err)
	}
}

// 折损取整：充入电量按折损比例折算后向下取整计入荷电。
func TestChargeLossFloor(t *testing.T) {
	cfg := baseConfig()
	cfg.ChargeLossPermille = 500 // 折损 50%
	cfg.MaxSoC = 51
	c := newWithForecast(t, cfg, 3)
	if err := c.SubmitPlan(1, plan(chg(3))); err != nil { // floor(3*0.5)=1 → 51
		t.Fatalf("折算后恰到上限应被接受: %v", err)
	}
	mustReject(t, c.SubmitPlan(2, plan(chg(5))), ErrOutOfBounds, 2) // floor(2.5)=2 → 53>51
	advance(t, c, 1)
	if _, err := c.RecordActual(1, ActionCharge, 3); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if got := c.Snapshot().SoC; got != 51 {
		t.Fatalf("折损取整后荷电应为 51，得到 %d", got)
	}
	// 充 1 单位折算为 floor(0.5)=0。
	if _, err := c.RecordActual(2, ActionCharge, 1); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if got := c.Snapshot().SoC; got != 51 {
		t.Fatalf("充 1 折损后荷电应不变，得到 %d", got)
	}
}
