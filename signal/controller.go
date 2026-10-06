package signal

import "sync"

// Controller 路口信号控制器。所有公开方法持有互斥锁，
// 并发调用等价于某个串行顺序；相同操作序列重放结果完全一致。
type Controller struct {
	mu     sync.Mutex
	eng    *engine
	lastOp int64
	view   int64 // 已知最晚时刻（操作与查询的高水位）
}

// NewController 创建控制器；初始方案必须可行。t=0 时相位 0 绿灯起点。
func NewController(phases []Phase, initial Plan) (*Controller, error) {
	e, err := newEngine(phases, initial)
	if err != nil {
		return nil, err
	}
	return &Controller{eng: e}, nil
}

// ChangePlan 提交配时方案变更，在当前循环结束瞬间生效；
// 同循环内后接受的方案静默替换先前未生效方案。
func (c *Controller) ChangePlan(p Plan, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.eng
	if p.ID == "" || len(p.Greens) != e.n || p.Offset < 0 || p.MaxAdjust < 0 {
		return errf(ErrInvalidParam, "plan %+v malformed", p)
	}
	if _, dup := e.planStatus[p.ID]; dup {
		return errf(ErrInvalidParam, "plan id %q already used", p.ID)
	}
	if t < c.lastOp {
		return errf(ErrClockRollback, "t=%d < last accepted t=%d", t, c.lastOp)
	}
	if err := e.checkPlan(&p); err != nil {
		return err
	}
	c.lastOp = t
	c.view = max(c.view, t)
	e.advance(t)
	if e.pending != nil {
		e.planStatus[e.pending.ID] = PlanReplaced
	}
	p.Greens = append([]int(nil), p.Greens...)
	e.pending = &p
	e.planStatus[p.ID] = PlanPending
	return nil
}

// RequestEmergency 提交紧急优先请求。
func (c *Controller) RequestEmergency(id string, target int, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.eng
	if id == "" {
		return errf(ErrInvalidParam, "empty request id")
	}
	if t < c.lastOp {
		return errf(ErrClockRollback, "t=%d < last accepted t=%d", t, c.lastOp)
	}
	e.advance(t)
	if target < 0 || target >= e.n {
		return errf(ErrPhaseNotExist, "phase %d out of [0,%d)", target, e.n)
	}
	if e.seen[id] {
		return errf(ErrDuplicateRequest, "request id %q already submitted", id)
	}
	if e.skipViolation(target, t) {
		return errf(ErrConsecutiveSkip, "request %q would skip a phase in adjacent cycles", id)
	}
	if e.occupied(target) {
		return errf(ErrTargetOccupied, "phase %d occupied by another emergency request", target)
	}
	c.lastOp = t
	c.view = max(c.view, t)
	e.seen[id] = true
	e.addEmergency(emRequest{id: id, target: target, time: t})
	return nil
}

// ConfirmPass 紧急车辆通过确认：目标相位保持提前结束（不早于最小绿）。
// 对未知标识报参数非法；对已知但未处于保持期的请求为幂等空操作。
func (c *Controller) ConfirmPass(id string, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.eng
	if id == "" || !e.seen[id] {
		return errf(ErrInvalidParam, "unknown request id %q", id)
	}
	if t < c.lastOp {
		return errf(ErrClockRollback, "t=%d < last accepted t=%d", t, c.lastOp)
	}
	c.lastOp = t
	c.view = max(c.view, t)
	e.advance(t)
	if e.active != nil && e.active.req.id == id && e.active.stage == stHold && t < e.greenEnd() {
		ng := t - e.slotStart
		if mg := int64(e.phases[e.cur].MinGreen); ng < mg {
			ng = mg
		}
		if ng < e.greenDur {
			e.greenDur = ng
			e.cleanCycle = false
		}
	}
	return nil
}

// RequestBus 提交公交优先请求：延长或提前结束当前相位，不跳相。
// 与紧急请求同时有效时被抢占（不报错，状态可查为 Preempted）。
func (c *Controller) RequestBus(id string, mode BusMode, amount int, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.eng
	if id == "" || amount <= 0 || (mode != BusExtend && mode != BusShorten) {
		return errf(ErrInvalidParam, "bad bus request id=%q mode=%d amount=%d", id, mode, amount)
	}
	if t < c.lastOp {
		return errf(ErrClockRollback, "t=%d < last accepted t=%d", t, c.lastOp)
	}
	e.advance(t)
	if t >= e.greenEnd() {
		return errf(ErrInvalidParam, "bus request during clearance of phase %d", e.cur)
	}
	if e.seen[id] {
		return errf(ErrDuplicateRequest, "request id %q already submitted", id)
	}
	c.lastOp = t
	c.view = max(c.view, t)
	e.seen[id] = true
	if e.active != nil || len(e.queue) > 0 {
		e.busStatus[id] = BusPreempted
		return nil
	}
	switch mode {
	case BusExtend:
		e.greenDur = min(e.greenDur+int64(amount), int64(e.phases[e.cur].MaxGreen))
	case BusShorten:
		e.greenDur = max(e.greenDur-int64(amount), int64(e.phases[e.cur].MinGreen), t-e.slotStart)
	}
	e.cleanCycle = false
	e.busStatus[id] = BusCompleted
	return nil
}

// Query 查询任意（不小于最后接受操作）时刻的状态快照。
// 在状态副本上推进，不改变控制器状态；开销不随已过去循环数增长。
func (c *Controller) Query(t int64) (QueryResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < c.lastOp {
		return QueryResult{}, errf(ErrClockRollback, "t=%d < last accepted t=%d", t, c.lastOp)
	}
	s := c.eng.clone()
	s.advance(t)
	c.view = max(c.view, t)
	return QueryResult{
		Time:        t,
		Phase:       s.cur,
		Elapsed:     t - s.slotStart,
		Remaining:   s.slotEnd() - t,
		InClearance: t >= s.greenEnd(),
		Deviation:   s.deviation(),
		Cycle:       s.cycleIdx,
	}, nil
}

// snapshot 返回推进到高水位时刻的状态副本（状态查询专用，不改动物化状态）。
func (c *Controller) snapshot() *engine {
	s := c.eng.clone()
	s.advance(c.view)
	return s
}

// EmergencyStatus 查询紧急请求状态；未受理（含被拒绝）返回 false。
func (c *Controller) EmergencyStatus(id string) (EmStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.snapshot().emStatus[id]
	return s, ok
}

// BusStatus 查询公交请求状态；未受理（含被拒绝）返回 false。
func (c *Controller) BusStatus(id string) (BusStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.snapshot().busStatus[id]
	return s, ok
}

// PlanStatus 查询方案状态（含被静默替换）；未知标识返回 false。
func (c *Controller) PlanStatus(id string) (PlanStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.snapshot().planStatus[id]
	return s, ok
}
