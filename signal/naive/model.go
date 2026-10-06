// Package naive 是独立编写的逐秒推进参照模型，
// 用于与 signal 包的事件驱动实现交叉验证。
// 语义与 signal 完全一致，但推进方式为每秒一步。
package naive

import (
	"fmt"

	"ontology/signal"
)

type stage int

const (
	stWaitGreen stage = iota + 1
	stApproach
	stHold
	stFinish
)

type emReq struct {
	id     string
	target int
	time   int64
}

type service struct {
	req   emReq
	stage stage
}

// Controller 逐秒推进的信号控制参照模型。
type Controller struct {
	phases []signal.Phase
	n      int

	plan     signal.Plan
	pending  *signal.Plan
	prefix   []int64
	cycleLen int64

	now        int64
	lastOp     int64
	cycleIdx   int64
	cycleStart int64
	cur        int
	slotStart  int64
	greenDur   int64
	cycGreen   []int64

	queue   []emReq
	active  *service
	skipped map[int]int64

	emStatus   map[string]signal.EmStatus
	busStatus  map[string]signal.BusStatus
	planStatus map[string]signal.PlanStatus
	seen       map[string]bool
}

func NewController(phases []signal.Phase, plan signal.Plan) (*Controller, error) {
	if len(phases) < 2 {
		return nil, fmt.Errorf("invalid-param: need at least 2 phases")
	}
	for i, p := range phases {
		if p.MinGreen < 1 || p.MaxGreen < p.MinGreen || p.Clear < 1 {
			return nil, fmt.Errorf("invalid-param: phase %d", i)
		}
	}
	c := &Controller{
		phases:     append([]signal.Phase(nil), phases...),
		n:          len(phases),
		skipped:    map[int]int64{},
		emStatus:   map[string]signal.EmStatus{},
		busStatus:  map[string]signal.BusStatus{},
		planStatus: map[string]signal.PlanStatus{},
		seen:       map[string]bool{},
	}
	if err := c.checkPlan(&plan); err != nil {
		return nil, err
	}
	c.plan = plan
	c.plan.Greens = append([]int(nil), plan.Greens...)
	c.recompute()
	c.cycGreen = make([]int64, c.n)
	for i := range c.cycGreen {
		c.cycGreen[i] = int64(plan.Greens[i])
	}
	c.greenDur = c.cycGreen[0]
	c.planStatus[plan.ID] = signal.PlanActive
	return c, nil
}

func (c *Controller) checkPlan(p *signal.Plan) error {
	if p.ID == "" || len(p.Greens) != c.n || p.Offset < 0 || p.MaxAdjust < 0 {
		return fmt.Errorf("invalid-param: malformed plan")
	}
	if _, dup := c.planStatus[p.ID]; dup {
		return fmt.Errorf("invalid-param: plan id reused")
	}
	var cyc int64
	for i, g := range p.Greens {
		if g < c.phases[i].MinGreen || g > c.phases[i].MaxGreen {
			return fmt.Errorf("infeasible-plan: phase %d green %d", i, g)
		}
		cyc += int64(g) + int64(c.phases[i].Clear)
	}
	if p.Offset >= cyc {
		return fmt.Errorf("infeasible-plan: offset >= cycle")
	}
	return nil
}

func (c *Controller) recompute() {
	c.prefix = make([]int64, c.n)
	var acc int64
	for i := 0; i < c.n; i++ {
		c.prefix[i] = acc
		acc += int64(c.plan.Greens[i]) + int64(c.phases[i].Clear)
	}
	c.cycleLen = acc
}

func normDev(x, m int64) int64 {
	r := x % m
	if r < 0 {
		r += m
	}
	if 2*r > m {
		r -= m
	}
	return r
}

func (c *Controller) greenEnd() int64 { return c.slotStart + c.greenDur }

func (c *Controller) slotEnd() int64 {
	return c.greenEnd() + int64(c.phases[c.cur].Clear)
}

// tick 推进一秒：先处理保持结束，再处理槽结束。
func (c *Controller) tick() {
	t := c.now + 1
	if c.active != nil && c.active.stage == stHold && t == c.greenEnd() {
		c.active.stage = stFinish
	}
	if t == c.slotEnd() {
		c.onSlotEnd(t)
	}
	c.now = t
}

func (c *Controller) tickTo(t int64) {
	for c.now < t {
		c.tick()
	}
}

func (c *Controller) onSlotEnd(t int64) {
	cur := c.cur
	jump := c.active != nil && c.active.stage == stApproach
	var next int
	if jump {
		next = c.active.req.target
		c.active.stage = stHold
	} else {
		next = (cur + 1) % c.n
	}
	if next <= cur {
		c.cycleIdx++
		c.cycleStart = t
		c.applyPending()
		c.adjust(next, t)
	}
	if jump {
		for p := (cur + 1) % c.n; p != next; p = (p + 1) % c.n {
			c.skipped[p] = c.cycleIdx
		}
	}
	c.cur = next
	c.slotStart = t
	if jump {
		c.greenDur = int64(c.phases[next].MaxGreen)
	} else {
		c.greenDur = c.cycGreen[next]
	}
	if c.active != nil && c.active.stage == stFinish {
		c.emStatus[c.active.req.id] = signal.EmCompleted
		c.active = nil
	}
	c.maybeStart(t)
	if c.active != nil && c.active.stage == stWaitGreen {
		c.startLogic(t)
	}
}

func (c *Controller) maybeStart(t int64) {
	if c.active != nil || len(c.queue) == 0 {
		return
	}
	r := c.queue[0]
	c.queue = c.queue[1:]
	c.active = &service{req: r, stage: stWaitGreen}
	c.emStatus[r.id] = signal.EmActive
	if t < c.greenEnd() {
		c.startLogic(t)
	}
}

func (c *Controller) startLogic(t int64) {
	p := c.cur
	if p == c.active.req.target {
		c.greenDur = int64(c.phases[p].MaxGreen)
		c.active.stage = stHold
		return
	}
	if mg := int64(c.phases[p].MinGreen); c.greenDur > mg {
		c.greenDur = mg
	}
	if el := t - c.slotStart; c.greenDur < el {
		c.greenDur = el
	}
	c.active.stage = stApproach
}

func (c *Controller) applyPending() {
	if c.pending == nil {
		return
	}
	c.planStatus[c.plan.ID] = signal.PlanReplaced
	c.plan = *c.pending
	c.pending = nil
	c.planStatus[c.plan.ID] = signal.PlanActive
	c.recompute()
}

func (c *Controller) adjust(p int, t int64) {
	for i := range c.cycGreen {
		c.cycGreen[i] = int64(c.plan.Greens[i])
	}
	d := normDev(t-c.plan.Offset-c.prefix[p], c.cycleLen)
	if d == 0 || c.plan.MaxAdjust <= 0 {
		return
	}
	budget := c.plan.MaxAdjust
	if d < 0 {
		if -d < budget {
			budget = -d
		}
	} else if d < budget {
		budget = d
	}
	extend := d < 0 || 2*d == c.cycleLen
	rem := budget
	for i := 0; i < c.n && rem > 0; i++ {
		var can int64
		if extend {
			can = int64(c.phases[i].MaxGreen) - c.cycGreen[i]
		} else {
			can = c.cycGreen[i] - int64(c.phases[i].MinGreen)
		}
		take := min(can, rem)
		if extend {
			c.cycGreen[i] += take
		} else {
			c.cycGreen[i] -= take
		}
		rem -= take
	}
}

func (c *Controller) ChangePlan(p signal.Plan, t int64) error {
	if p.ID == "" || len(p.Greens) != c.n || p.Offset < 0 || p.MaxAdjust < 0 {
		return fmt.Errorf("invalid-param: malformed plan")
	}
	if _, dup := c.planStatus[p.ID]; dup {
		return fmt.Errorf("invalid-param: plan id reused")
	}
	if t < c.lastOp {
		return fmt.Errorf("clock-rollback: t=%d", t)
	}
	if err := c.checkPlan(&p); err != nil {
		return err
	}
	c.lastOp = t
	c.tickTo(t)
	if c.pending != nil {
		c.planStatus[c.pending.ID] = signal.PlanReplaced
	}
	p.Greens = append([]int(nil), p.Greens...)
	c.pending = &p
	c.planStatus[p.ID] = signal.PlanPending
	return nil
}

func (c *Controller) RequestEmergency(id string, target int, t int64) error {
	if id == "" {
		return fmt.Errorf("invalid-param: empty id")
	}
	if t < c.lastOp {
		return fmt.Errorf("clock-rollback: t=%d", t)
	}
	c.tickTo(t)
	if target < 0 || target >= c.n {
		return fmt.Errorf("phase-not-exist: %d", target)
	}
	if c.seen[id] {
		return fmt.Errorf("duplicate-request: %s", id)
	}
	if c.skipViolation(target, t) {
		return fmt.Errorf("consecutive-skip: %s", id)
	}
	if c.occupied(target) {
		return fmt.Errorf("target-occupied: %d", target)
	}
	c.lastOp = t
	c.seen[id] = true
	r := emReq{id: id, target: target, time: t}
	i := len(c.queue)
	for i > 0 && (c.queue[i-1].time > r.time ||
		(c.queue[i-1].time == r.time && c.queue[i-1].id > r.id)) {
		i--
	}
	c.queue = append(c.queue, emReq{})
	copy(c.queue[i+1:], c.queue[i:])
	c.queue[i] = r
	c.emStatus[id] = signal.EmQueued
	c.maybeStart(t)
	return nil
}

func (c *Controller) skipViolation(target int, t int64) bool {
	var eff int
	switch {
	case c.active == nil && len(c.queue) == 0:
		if t < c.greenEnd() {
			eff = c.cur
		} else {
			eff = (c.cur + 1) % c.n
		}
	default:
		last := c.active.req.target
		if len(c.queue) > 0 {
			last = c.queue[len(c.queue)-1].target
		}
		eff = (last + 1) % c.n
	}
	for p := (eff + 1) % c.n; p != target; p = (p + 1) % c.n {
		if cyc, ok := c.skipped[p]; ok && cyc >= c.cycleIdx-1 {
			return true
		}
	}
	return false
}

func (c *Controller) occupied(target int) bool {
	if c.active != nil && c.active.req.target == target {
		return true
	}
	for _, r := range c.queue {
		if r.target == target {
			return true
		}
	}
	return false
}

func (c *Controller) ConfirmPass(id string, t int64) error {
	if id == "" || !c.seen[id] {
		return fmt.Errorf("invalid-param: unknown id %q", id)
	}
	if t < c.lastOp {
		return fmt.Errorf("clock-rollback: t=%d", t)
	}
	c.lastOp = t
	c.tickTo(t)
	if c.active != nil && c.active.req.id == id && c.active.stage == stHold && t < c.greenEnd() {
		ng := t - c.slotStart
		if mg := int64(c.phases[c.cur].MinGreen); ng < mg {
			ng = mg
		}
		if ng < c.greenDur {
			c.greenDur = ng
		}
	}
	return nil
}

func (c *Controller) RequestBus(id string, mode signal.BusMode, amount int, t int64) error {
	if id == "" || amount <= 0 || (mode != signal.BusExtend && mode != signal.BusShorten) {
		return fmt.Errorf("invalid-param: bad bus request")
	}
	if t < c.lastOp {
		return fmt.Errorf("clock-rollback: t=%d", t)
	}
	c.tickTo(t)
	if t >= c.greenEnd() {
		return fmt.Errorf("invalid-param: bus request during clearance")
	}
	if c.seen[id] {
		return fmt.Errorf("duplicate-request: %s", id)
	}
	c.lastOp = t
	c.seen[id] = true
	if c.active != nil || len(c.queue) > 0 {
		c.busStatus[id] = signal.BusPreempted
		return nil
	}
	switch mode {
	case signal.BusExtend:
		c.greenDur = min(c.greenDur+int64(amount), int64(c.phases[c.cur].MaxGreen))
	case signal.BusShorten:
		c.greenDur = max(c.greenDur-int64(amount), int64(c.phases[c.cur].MinGreen), t-c.slotStart)
	}
	c.busStatus[id] = signal.BusCompleted
	return nil
}

func (c *Controller) Query(t int64) (signal.QueryResult, error) {
	if t < c.lastOp {
		return signal.QueryResult{}, fmt.Errorf("clock-rollback: t=%d", t)
	}
	c.tickTo(t)
	return signal.QueryResult{
		Time:        t,
		Phase:       c.cur,
		Elapsed:     t - c.slotStart,
		Remaining:   c.slotEnd() - t,
		InClearance: t >= c.greenEnd(),
		Deviation:   normDev(c.slotStart-c.plan.Offset-c.prefix[c.cur], c.cycleLen),
		Cycle:       c.cycleIdx,
	}, nil
}

func (c *Controller) EmergencyStatus(id string) (signal.EmStatus, bool) {
	s, ok := c.emStatus[id]
	return s, ok
}

func (c *Controller) BusStatus(id string) (signal.BusStatus, bool) {
	s, ok := c.busStatus[id]
	return s, ok
}

func (c *Controller) PlanStatus(id string) (signal.PlanStatus, bool) {
	s, ok := c.planStatus[id]
	return s, ok
}
