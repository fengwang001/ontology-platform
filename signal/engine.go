package signal

// engine 事件驱动的核心状态机。所有状态变化只发生在槽边界（绿结束/槽结束）
// 与操作时刻；advance 通过事件跳跃推进，配合稳态周期快跳保证 O(1) 查询。

type emRequest struct {
	id     string
	target int
	time   int64
}

type emStage int

const (
	stWaitGreen emStage = iota // 等待当前清空结束、下一个绿灯起点
	stApproach                 // 当前相位跑满最小绿后跳向目标
	stHold                     // 目标相位保持（至通过确认或最大绿）
	stFinish                   // 保持结束，处于目标相位清空段
)

type emService struct {
	req   emRequest
	stage emStage
}

type engine struct {
	phases []Phase
	n      int

	plan     Plan
	prefix   []int64 // 各相位计划起点在周期内的前缀位移
	cycleLen int64
	pending  *Plan

	now        int64
	cycleIdx   int64
	cycleStart int64
	cur        int
	slotStart  int64
	greenDur   int64
	cycGreen   []int64 // 本循环工作绿时（回归调整后的）
	lastAdjust int64   // 上一次回绕时实际应用的调整量
	cleanCycle bool    // 当前循环未被中途修改且长度等于周期，可整循环快跳
	steady     bool    // 上一回绕无调整且下一回绕也不会调整（纯周期运行）

	queue   []emRequest // 按 (请求时刻, 标识字典序) 排序
	active  *emService
	skipped map[int]int64 // 相位 -> 最近一次被跳过时的循环序号

	emStatus   map[string]EmStatus
	busStatus  map[string]BusStatus
	planStatus map[string]PlanStatus
	seen       map[string]bool

	events int64 // 已处理事件数（用于查询复杂度验证）
}

func newEngine(phases []Phase, plan Plan) (*engine, error) {
	if len(phases) < 2 {
		return nil, errf(ErrInvalidParam, "need at least 2 phases, got %d", len(phases))
	}
	for i, p := range phases {
		if p.MinGreen < 1 || p.MaxGreen < p.MinGreen || p.Clear < 1 {
			return nil, errf(ErrInvalidParam, "phase %d invalid constraints %+v", i, p)
		}
	}
	e := &engine{
		phases:     append([]Phase(nil), phases...),
		n:          len(phases),
		skipped:    map[int]int64{},
		emStatus:   map[string]EmStatus{},
		busStatus:  map[string]BusStatus{},
		planStatus: map[string]PlanStatus{},
		seen:       map[string]bool{},
	}
	if err := e.checkPlan(&plan); err != nil {
		return nil, err
	}
	e.plan = plan
	e.plan.Greens = append([]int(nil), plan.Greens...)
	e.computePrefix()
	e.cycGreen = make([]int64, e.n)
	for i := range e.cycGreen {
		e.cycGreen[i] = int64(e.plan.Greens[i])
	}
	e.greenDur = e.cycGreen[0]
	e.planStatus[plan.ID] = PlanActive
	e.cleanCycle = true
	e.steady = !e.nextWrapAdjusts()
	return e, nil
}

// nextWrapAdjusts 预测下一个回绕（相位0起点）是否会应用回归调整。
func (e *engine) nextWrapAdjusts() bool {
	t := e.cycleStart + e.cycleLen
	d := normDev(t-e.plan.Offset-e.prefix[0], e.cycleLen)
	if d == 0 || e.plan.MaxAdjust <= 0 {
		return false
	}
	extend := d < 0 || 2*d == e.cycleLen
	for i := 0; i < e.n; i++ {
		if extend && int64(e.phases[i].MaxGreen) > int64(e.plan.Greens[i]) {
			return true
		}
		if !extend && int64(e.plan.Greens[i]) > int64(e.phases[i].MinGreen) {
			return true
		}
	}
	return false
}

// checkPlan 校验方案结构（参数非法）与可行性（方案不可行）。
func (e *engine) checkPlan(p *Plan) error {
	if p.ID == "" || len(p.Greens) != e.n || p.Offset < 0 || p.MaxAdjust < 0 {
		return errf(ErrInvalidParam, "plan %+v malformed", p)
	}
	if _, dup := e.planStatus[p.ID]; dup {
		return errf(ErrInvalidParam, "plan id %q already used", p.ID)
	}
	var cycle int64
	for i, g := range p.Greens {
		if int64(g) < int64(e.phases[i].MinGreen) || int64(g) > int64(e.phases[i].MaxGreen) {
			return errf(ErrInfeasiblePlan, "green %d out of [%d,%d] for phase %d",
				g, e.phases[i].MinGreen, e.phases[i].MaxGreen, i)
		}
		cycle += int64(g) + int64(e.phases[i].Clear)
	}
	if p.Offset >= cycle {
		return errf(ErrInfeasiblePlan, "offset %d must be < cycle %d", p.Offset, cycle)
	}
	return nil
}

func (e *engine) computePrefix() {
	e.prefix = make([]int64, e.n)
	var acc int64
	for i := 0; i < e.n; i++ {
		e.prefix[i] = acc
		acc += int64(e.plan.Greens[i]) + int64(e.phases[i].Clear)
	}
	e.cycleLen = acc
}

// deviation 当前相位实际起点相对计划起点的归一化偏离。
func (e *engine) deviation() int64 {
	return normDev(e.slotStart-e.plan.Offset-e.prefix[e.cur], e.cycleLen)
}

func (e *engine) greenEnd() int64 { return e.slotStart + e.greenDur }

func (e *engine) slotEnd() int64 {
	return e.greenEnd() + int64(e.phases[e.cur].Clear)
}

func (e *engine) clone() *engine {
	c := *e
	c.phases = append([]Phase(nil), e.phases...)
	c.prefix = append([]int64(nil), e.prefix...)
	c.cycGreen = append([]int64(nil), e.cycGreen...)
	if e.pending != nil {
		p := *e.pending
		p.Greens = append([]int(nil), e.pending.Greens...)
		c.pending = &p
	}
	c.queue = append([]emRequest(nil), e.queue...)
	if e.active != nil {
		a := *e.active
		c.active = &a
	}
	c.skipped = mapsClone(e.skipped)
	c.emStatus = mapsClone(e.emStatus)
	c.busStatus = mapsClone(e.busStatus)
	c.planStatus = mapsClone(e.planStatus)
	c.seen = mapsClone(e.seen)
	return &c
}

func mapsClone[K comparable, V any](m map[K]V) map[K]V {
	c := make(map[K]V, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// advance 推进到时刻 t（处理所有 <= t 的事件）。
func (e *engine) advance(t int64) {
	for {
		// 稳态快跳：无紧急活动、无待生效方案、当前循环干净（长度=周期）时，
		// 系统以 cycleLen 为周期纯周期运行，可算术跳过整循环。
		if e.cleanCycle && e.steady && e.active == nil && len(e.queue) == 0 && e.pending == nil {
			if dt := t - e.cycleStart; dt >= e.cycleLen {
				k := dt / e.cycleLen
				e.cycleStart += k * e.cycleLen
				e.cycleIdx += k
				e.cur = 0
				e.slotStart = e.cycleStart
				e.greenDur = e.cycGreen[0]
				e.events++
				continue
			}
		}
		ev := e.nextEvent()
		if ev > t {
			break
		}
		e.process(ev)
	}
	e.now = t
}

func (e *engine) nextEvent() int64 {
	if e.active != nil && e.active.stage == stHold {
		return e.greenEnd()
	}
	return e.slotEnd()
}

func (e *engine) process(ev int64) {
	e.events++
	if e.active != nil && e.active.stage == stHold && ev == e.greenEnd() {
		e.active.stage = stFinish
		return
	}
	// 槽结束：决定下一个相位。
	cur := e.cur
	jump := e.active != nil && e.active.stage == stApproach
	var next int
	if jump {
		next = e.active.req.target
		e.active.stage = stHold
	} else {
		next = (cur + 1) % e.n
	}
	if next <= cur { // 循环回绕
		e.cycleIdx++
		e.cycleStart = ev
		e.applyPending()
		e.adjustCycle(next, ev)
		e.cleanCycle = !jump && e.lastAdjust == 0
		e.steady = e.lastAdjust == 0 && !e.nextWrapAdjusts()
	}
	if jump { // 记录被跳过相位，归属新循环序号
		for p := (cur + 1) % e.n; p != next; p = (p + 1) % e.n {
			e.skipped[p] = e.cycleIdx
		}
	}
	e.cur = next
	e.slotStart = ev
	if jump {
		e.greenDur = int64(e.phases[next].MaxGreen)
	} else {
		e.greenDur = e.cycGreen[next]
	}
	if e.active != nil && e.active.stage == stFinish {
		e.emStatus[e.active.req.id] = EmCompleted
		e.active = nil
	}
	e.maybeStartService(ev)
	if e.active != nil && e.active.stage == stWaitGreen {
		e.startLogic(ev)
	}
}

// maybeStartService 队首请求在无活动服务时开始服务。
func (e *engine) maybeStartService(t int64) {
	if e.active != nil || len(e.queue) == 0 {
		return
	}
	r := e.queue[0]
	e.queue = e.queue[1:]
	e.active = &emService{req: r, stage: stWaitGreen}
	e.emStatus[r.id] = EmActive
	if t < e.greenEnd() { // 当前处于绿灯段，立即处理
		e.startLogic(t)
	}
}

// startLogic 服务开始：目标即当前相位则延长到最大绿，否则跑满最小绿后跳相。
func (e *engine) startLogic(t int64) {
	p := e.cur
	e.cleanCycle = false
	if p == e.active.req.target {
		e.greenDur = int64(e.phases[p].MaxGreen)
		e.active.stage = stHold
		return
	}
	if mg := int64(e.phases[p].MinGreen); e.greenDur > mg {
		e.greenDur = mg
	}
	if elapsed := t - e.slotStart; e.greenDur < elapsed {
		e.greenDur = elapsed
	}
	e.active.stage = stApproach
}

// applyPending 循环结束瞬间生效待生效方案。
func (e *engine) applyPending() {
	if e.pending == nil {
		return
	}
	e.planStatus[e.plan.ID] = PlanReplaced
	e.plan = *e.pending
	e.pending = nil
	e.planStatus[e.plan.ID] = PlanActive
	e.computePrefix()
}

// addEmergency 受理请求：入队（保持 (时刻,标识) 有序）并尝试立即开始服务。
func (e *engine) addEmergency(r emRequest) {
	i := len(e.queue)
	for i > 0 && (e.queue[i-1].time > r.time ||
		(e.queue[i-1].time == r.time && e.queue[i-1].id > r.id)) {
		i--
	}
	e.queue = append(e.queue, emRequest{})
	copy(e.queue[i+1:], e.queue[i:])
	e.queue[i] = r
	e.emStatus[r.id] = EmQueued
	e.maybeStartService(e.now)
}

// skipViolation 预测服务开始时有效的当前相位，检查连续跳相约束。
func (e *engine) skipViolation(target int, t int64) bool {
	var effCur int
	switch {
	case e.active == nil && len(e.queue) == 0:
		if t < e.greenEnd() {
			effCur = e.cur
		} else {
			effCur = (e.cur + 1) % e.n
		}
	default: // 前一服务结束于其目标相位，其后继即本服务的有效当前相位
		last := e.active.req.target
		if len(e.queue) > 0 {
			last = e.queue[len(e.queue)-1].target
		}
		effCur = (last + 1) % e.n
	}
	for p := (effCur + 1) % e.n; p != target; p = (p + 1) % e.n {
		if c, ok := e.skipped[p]; ok && c >= e.cycleIdx-1 {
			return true
		}
	}
	return false
}

// occupied 目标相位是否正被活动或排队的紧急请求占用。
func (e *engine) occupied(target int) bool {
	if e.active != nil && e.active.req.target == target {
		return true
	}
	for _, r := range e.queue {
		if r.target == target {
			return true
		}
	}
	return false
}
