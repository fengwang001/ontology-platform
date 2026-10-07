package microgrid

import (
	"maps"
	"slices"
	"sync"
)

// RevokeReason 标识一次撤销的触发源。
type RevokeReason string

const (
	ReasonForecastUpdate RevokeReason = "forecast-update"
	ReasonDeviation      RevokeReason = "deviation"
	ReasonIslandSwitch   RevokeReason = "island-switch"
	ReasonSurplusUpdate  RevokeReason = "surplus-update"
	ReasonMaintenance    RevokeReason = "maintenance-lock"
)

// Revocation 记录一次时隙撤销，Seq 全局递增以保证重放一致。
type Revocation struct {
	Seq    int
	Slot   int
	Reason RevokeReason
}

// Snapshot 是控制器状态的一致性快照，用于观测与测试。
type Snapshot struct {
	SoC             int
	Current         int
	Mode            Mode
	Locked          bool
	Throughput      int
	Accepted        map[int]PlanAction
	ReplaySteps     int // 累计推演时隙数（性能验证用）
	ForecastQueries int // 备用判定累计预测访问次数（性能验证用）
}

// Controller 是微电网储能调度控制器。所有方法可并发调用，
// 内部以单互斥锁串行化，效果等价于某个串行执行顺序。
type Controller struct {
	mu          sync.Mutex
	cfg         Config
	soc         int
	current     int
	mode        Mode
	locked      bool
	throughput  int
	fc          *forecastBook
	plans       *planBook
	revLog      []Revocation
	replaySteps int
}

// NewController 校验参数并构造控制器；参数非法时返回 ErrInvalidParam 类错误。
func NewController(cfg Config) (*Controller, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Controller{
		cfg:   cfg,
		soc:   cfg.InitialSoC,
		mode:  ModeGrid,
		fc:    newForecastBook(),
		plans: newPlanBook(),
	}, nil
}

// checkSlot 校验单个时隙，通过时推进 *soc。
// 同一时隙多类不满足时按固定次序报告：维护锁定 > 模式不允许 > 越界 > 备用不足 > 预测缺失。
func (c *Controller) checkSlot(slot int, a PlanAction, soc *int) (ErrKind, bool) {
	c.replaySteps++
	if c.locked && a.Action == ActionDischarge && a.Amount > 0 &&
		!(c.mode == ModeIsland && a.Amount <= c.fc.forecastOr(slot, 0)) {
		return ErrMaintenanceLock, true
	}
	if c.mode == ModeIsland {
		switch a.Action {
		case ActionDischarge:
			if a.Amount > c.fc.forecastOr(slot, 0) {
				return ErrModeNotAllowed, true
			}
		case ActionCharge:
			if a.Amount > c.fc.surplusAt(slot) {
				return ErrModeNotAllowed, true
			}
		}
	}
	next := applyAction(c.cfg, *soc, a)
	if next < c.cfg.MinSoC || next > c.cfg.MaxSoC {
		return ErrOutOfBounds, true
	}
	*soc = next
	if next-c.cfg.MinSoC < c.fc.reserveNeed(slot, c.cfg.ReserveHorizon) {
		return ErrReserveShortfall, true
	}
	if _, ok := c.fc.forecastAt(slot); !ok {
		return ErrForecastMissing, true
	}
	return 0, false
}

// replay 从给定荷电出发按升序推演全部时隙，返回首个不满足的时隙与类别。
func (c *Controller) replay(soc int, acts map[int]PlanAction) (int, ErrKind, bool) {
	for _, s := range slices.Sorted(maps.Keys(acts)) {
		if kind, bad := c.checkSlot(s, acts[s], &soc); bad {
			return s, kind, true
		}
	}
	return 0, 0, false
}

// revalidate 从当前荷电重新推演全部已接受且未执行的时隙，
// 自首个不满足的时隙起撤销其及之后全部已接受时隙。调用方须持有锁。
func (c *Controller) revalidate(reason RevokeReason) []int {
	slot, _, bad := c.replay(c.soc, c.plans.acts)
	if !bad {
		return nil
	}
	var revoked []int
	for _, s := range c.plans.slots() {
		if s >= slot {
			delete(c.plans.acts, s)
			c.logRevocation(s, reason)
			revoked = append(revoked, s)
		}
	}
	return revoked
}

func (c *Controller) logRevocation(slot int, reason RevokeReason) {
	c.revLog = append(c.revLog, Revocation{Seq: len(c.revLog), Slot: slot, Reason: reason})
}

// SubmitPlan 提交从 start 开始的连续未来时隙计划。
// 覆盖范围内旧计划被替换，范围外旧计划保留并随新计划一起重新推演；
// 任一条件不满足则整段拒绝，返回首个不满足的时隙与原因，且不改变任何状态。
func (c *Controller) SubmitPlan(start int, acts []PlanAction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(acts) == 0 {
		return &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "计划为空"}
	}
	if start <= c.current {
		return &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "起始时隙不是未来时隙"}
	}
	for i, a := range acts {
		if msg := validateAction(c.cfg, a); msg != "" {
			return &RejectError{Kind: ErrInvalidParam, Slot: start + i, Msg: msg}
		}
	}
	if slot, kind, bad := c.replay(c.soc, c.plans.mergedWith(start, acts)); bad {
		return &RejectError{Kind: kind, Slot: slot, Msg: "推演不满足接受条件"}
	}
	c.plans.replaceRange(start, start+len(acts)-1, acts)
	return nil
}

// UpdateForecast 更新从 start 开始的连续时隙的关键负荷预测（可整段或逐时隙），
// 随后对未执行计划重新推演并撤销不再满足的后缀。预测更新本身不会被拒绝。
func (c *Controller) UpdateForecast(start int, values []int) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(values) == 0 {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "预测为空"}
	}
	if start <= c.current {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: start, Msg: "只能更新未来时隙"}
	}
	for i, v := range values {
		if v < 0 {
			return nil, &RejectError{Kind: ErrInvalidParam, Slot: start + i, Msg: "预测值为负"}
		}
	}
	for i, v := range values {
		c.fc.setForecast(start+i, v)
	}
	return c.revalidate(ReasonForecastUpdate), nil
}

// RecordActual 登记当前时隙的实际充放电并推进时隙。
// 实际值使荷电越界时拒绝登记且时隙不推进；偏差超过容忍量时按更新后的荷电重推演。
func (c *Controller) RecordActual(slot int, action Action, amount int) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slot < 0 {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "时隙编号非法"}
	}
	if !action.valid() || amount < 0 || (action == ActionIdle && amount != 0) {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "实际电量非法"}
	}
	if slot != c.current {
		return nil, &RejectError{Kind: ErrSlotMismatch, Slot: slot, Msg: "只能登记当前时隙"}
	}
	a := PlanAction{Action: action, Amount: amount}
	next := applyAction(c.cfg, c.soc, a)
	if next < c.cfg.MinSoC || next > c.cfg.MaxSoC {
		return nil, &RejectError{Kind: ErrOutOfBounds, Slot: slot, Msg: "实际值使荷电越界"}
	}
	planned := c.plans.acts[slot] // 无计划时按闲置计
	delete(c.plans.acts, slot)
	c.fc.forget(slot)
	c.soc = next
	if action == ActionDischarge {
		c.throughput += amount
	}
	var revoked []int
	if !c.locked && c.throughput >= c.cfg.MaintenanceThreshold {
		c.locked = true
		revoked = append(revoked, c.revokeLockedDischarges()...)
	}
	if d := signedAmount(a) - signedAmount(planned); d > c.cfg.DeviationTolerance || -d > c.cfg.DeviationTolerance {
		revoked = append(revoked, c.revalidate(ReasonDeviation)...)
	}
	c.current++
	return revoked, nil
}

// revokeLockedDischarges 撤销未豁免的放电时隙并重新推演其余计划。
// 孤岛模式下不超过关键负荷预测的放电是唯一豁免。调用方须持有锁。
func (c *Controller) revokeLockedDischarges() []int {
	var revoked []int
	for _, s := range c.plans.slots() {
		a := c.plans.acts[s]
		if a.Action == ActionDischarge && a.Amount > 0 &&
			!(c.mode == ModeIsland && a.Amount <= c.fc.forecastOr(s, 0)) {
			delete(c.plans.acts, s)
			c.logRevocation(s, ReasonMaintenance)
			revoked = append(revoked, s)
		}
	}
	return append(revoked, c.revalidate(ReasonMaintenance)...)
}

// SetMode 在时隙边界切换运行模式。切换到孤岛时按孤岛规则重新推演并撤销不满足者；
// 切换到并网不引起撤销。
func (c *Controller) SetMode(m Mode) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m != ModeGrid && m != ModeIsland {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: -1, Msg: "未知运行模式"}
	}
	if m == c.mode {
		return nil, nil
	}
	c.mode = m
	if m == ModeIsland {
		return c.revalidate(ReasonIslandSwitch), nil
	}
	return nil, nil
}

// RegisterSurplus 登记某未来时隙的本地发电盈余（缺省为零）。
// 孤岛模式下盈余收紧可能使已接受计划失效，此时重新推演并撤销。
func (c *Controller) RegisterSurplus(slot, amount int) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slot <= c.current {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "只能登记未来时隙"}
	}
	if amount < 0 {
		return nil, &RejectError{Kind: ErrInvalidParam, Slot: slot, Msg: "盈余为负"}
	}
	c.fc.setSurplus(slot, amount)
	if c.mode == ModeIsland {
		return c.revalidate(ReasonSurplusUpdate), nil
	}
	return nil, nil
}

// CompleteMaintenance 登记维护完成：累计吞吐清零并解除维护锁定。
func (c *Controller) CompleteMaintenance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.throughput = 0
	c.locked = false
}

// Snapshot 返回当前状态的一致性快照。
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		SoC:             c.soc,
		Current:         c.current,
		Mode:            c.mode,
		Locked:          c.locked,
		Throughput:      c.throughput,
		Accepted:        maps.Clone(c.plans.acts),
		ReplaySteps:     c.replaySteps,
		ForecastQueries: c.fc.queries,
	}
}

// Revocations 返回按发生顺序排列的撤销记录。
func (c *Controller) Revocations() []Revocation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.revLog)
}
