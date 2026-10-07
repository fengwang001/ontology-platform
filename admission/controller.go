package admission

import (
	"sort"
	"sync"
)

// Request 是一次服务端请求。
type Request struct {
	ID        string
	UserGroup string
	Verb      string
	Resource  string
	User      string
	Namespace string
	Seats     int64 // 占用席位数，1..10
}

// Decision 为新请求的准入结论。
type Decision int

const (
	DecisionRejected Decision = iota
	DecisionExecuted
	DecisionQueued
)

// SubmitResult 是提交请求的结果；拒绝时 Err 非空。
type SubmitResult struct {
	Decision Decision
	Level    string
	Flow     string
	Err      *AdmissionError
}

// Event 记录一次操作中产生的附带结果（超时拒绝、出队执行、热更新拒绝）。
type Event struct {
	Kind     string // "executed" | "timeout-rejected" | "update-rejected"
	ID       string
	Level    string
	Flow     string
	Seats    int64
	Deadline Time
}

// OpResult 是任意一次操作（提交/完成/更新）的完整可观测结果。
type OpResult struct {
	Submit *SubmitResult
	Events []Event
	Now    Time
	Err    *AdmissionError
}

// Controller 是并发安全的优先级与公平性准入控制器。
type Controller struct {
	mu sync.Mutex

	now    Time
	cfg    *compiledConfig
	seqGen int64

	// live 记录所有当前执行或排队的请求 ID（豁免与受限），用于重复检测。
	live map[string]*liveRequest

	// levels 只包含受限级别；豁免级别不占席位、不排队。
	levels map[string]*levelState
}

type liveRequest struct {
	id       string
	level    string
	flow     string
	seats    int64
	limited  bool
	executed bool
}

type levelState struct {
	name    string
	cfg     *LevelConfig
	nominal int64

	used  int64
	size  int64 // 排队总数（队列上限判定 O(1)）
	flows *flowQueues
	exp   *expiryHeap
}

func newLevelState(cfg *LevelConfig, nominal int64) *levelState {
	return &levelState{
		name:    cfg.Name,
		cfg:     cfg,
		nominal: nominal,
		flows:   newFlowQueues(),
		exp:     newExpiryHeap(),
	}
}

// NewController 用初始配置构造控制器。
func NewController(cfg *Config) (*Controller, error) {
	compiled, err := compileConfig(cfg)
	if err != nil {
		return nil, err
	}
	c := &Controller{
		live:   map[string]*liveRequest{},
		levels: map[string]*levelState{},
	}
	c.cfg = compiled
	for _, lv := range compiled.limited {
		c.levels[lv.Name] = newLevelState(lv, compiled.nominal[lv.Name])
	}
	return c, nil
}

// Submit 在 now 时刻提交一个请求。
func (c *Controller) Submit(req *Request, now Time) *OpResult {
	if err := validateRequest(req); err != nil {
		// 被拒绝的新请求不改变任何状态（包括时钟）。
		return &OpResult{Submit: &SubmitResult{Decision: DecisionRejected, Err: err}}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, dup := c.live[req.ID]; dup {
		return &OpResult{Submit: &SubmitResult{
			Decision: DecisionRejected,
			Err:      newError(ClassInvalidArgument, "duplicate request id: "+req.ID),
		}}
	}
	if now < c.now {
		return &OpResult{
			Submit: &SubmitResult{
				Decision: DecisionRejected,
				Err:      newError(ClassClockRewind, "now precedes last accepted time"),
			},
			Now: c.now,
		}
	}

	op := &OpResult{Now: now}
	// 被接受的操作先推进时钟，再让超时失效，再进行准入判定。
	c.now = now
	c.advanceExpiryAndPump(op)

	cr := c.classify(req)
	if cr == nil {
		op.Submit = &SubmitResult{Decision: DecisionRejected, Err: newError(ClassNoMatch, "no rule matches request")}
		return op
	}
	flow := c.flowKey(cr, req)

	if !cr.limited {
		// 豁免级别：立即执行，不占席位，不排队。
		c.live[req.ID] = &liveRequest{id: req.ID, level: cr.rule.TargetLevel, flow: flow, executed: true}
		op.Submit = &SubmitResult{Decision: DecisionExecuted, Level: cr.rule.TargetLevel, Flow: flow}
		return op
	}

	st := c.levels[cr.rule.TargetLevel]
	if st.nominal == 0 || req.Seats > st.nominal {
		op.Submit = &SubmitResult{
			Decision: DecisionRejected,
			Level:    st.name,
			Flow:     flow,
			Err:      newError(ClassInsufficientSeat, "request seats exceed level nominal seats"),
		}
		return op
	}

	if st.used+req.Seats <= st.nominal && st.size == 0 {
		// 级别无等待者且席位足够：立即执行。该提交之前没有等待者，
		// 因此不会改变任何已有排队者的顺序，也不触发额外出队。
		st.used += req.Seats
		c.live[req.ID] = &liveRequest{
			id: req.ID, level: st.name, flow: flow, seats: req.Seats, limited: true, executed: true,
		}
		op.Submit = &SubmitResult{Decision: DecisionExecuted, Level: st.name, Flow: flow}
		return op
	}

	// 有等待者必须排队（不得插队）；席位当前不足时也排队等待。
	if st.size >= st.cfg.QueueLimit {
		op.Submit = &SubmitResult{
			Decision: DecisionRejected,
			Level:    st.name,
			Flow:     flow,
			Err:      newError(ClassQueueFull, "level queue limit reached"),
		}
		return op
	}

	c.seqGen++
	w := &waiter{
		id:       req.ID,
		seats:    req.Seats,
		enqueued: now,
		deadline: now + Time(st.cfg.Timeout),
		flow:     flow,
		level:    st.name,
		seq:      c.seqGen,
	}
	node := st.flows.activateAppend(flow)
	st.flows.enqueueWaiter(node, w)
	st.exp.push(w)
	st.size++
	c.live[req.ID] = &liveRequest{id: req.ID, level: st.name, flow: flow, seats: req.Seats, limited: true}
	op.Submit = &SubmitResult{Decision: DecisionQueued, Level: st.name, Flow: flow}
	return op
}

// Complete 标记一个已执行请求结束，释放席位并尽量出队。
func (c *Controller) Complete(requestID string, now Time) *OpResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	op := &OpResult{Now: c.now}
	if now < c.now {
		op.Err = newError(ClassClockRewind, "now precedes last accepted time")
		return op
	}
	lr, ok := c.live[requestID]
	if !ok || !lr.executed {
		// 完成未知/未执行请求不改业务状态；时钟推进带来的超时仍须体现，
		// 但“释放席位”未发生，不因此让出队（超时本身会在 advance 中处理）。
		if now > c.now {
			c.now = now
			c.advanceExpiryAndPump(op)
			op.Now = now
		}
		return op
	}

	op.Now = now
	c.now = now
	c.advanceExpiryAndPump(op)

	delete(c.live, requestID)
	if lr.limited {
		st := c.levels[lr.level]
		st.used -= lr.seats
		c.pump(st, op)
	}
	return op
}

// UpdateConfig 在 now 时刻整份替换配置；非法配置整体不生效。
func (c *Controller) UpdateConfig(cfg *Config, now Time) (*OpResult, error) {
	compiled, err := compileConfig(cfg)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.now {
		return nil, newError(ClassClockRewind, "now precedes last accepted time")
	}

	// 合法性（语义）：在途/排队请求引用的级别必须仍以同种类别存在；
	// 受限级别新名义席位不得小于当前占用（保证占用永不超过名义席位）。
	anyRunningSeats := int64(0)
	for _, lr := range c.live {
		if lr.executed && lr.limited && lr.seats > anyRunningSeats {
			anyRunningSeats = lr.seats
		}
		if !lr.limited {
			if !compiled.exempt[lr.level] {
				return nil, newError(ClassInvalidArgument, "update removes/relabels exempt level with live requests: "+lr.level)
			}
			continue
		}
		if _, ok := compiled.limited[lr.level]; !ok {
			return nil, newError(ClassInvalidArgument, "update removes/relabels limited level with live requests: "+lr.level)
		}
		if lr.executed && compiled.nominal[lr.level] < anyRunningSeats {
			return nil, newError(ClassInvalidArgument, "update nominal seats below in-flight usage: "+lr.level)
		}
	}

	op := &OpResult{Now: now}
	c.now = now

	// 整份替换配置与名义席位；级别状态对象保留，排队者不重新分类。
	newStates := map[string]*levelState{}
	for _, lv := range compiled.limited {
		nominal := compiled.nominal[lv.Name]
		if old, ok := c.levels[lv.Name]; ok {
			old.cfg = lv
			old.nominal = nominal
			newStates[lv.Name] = old
		} else {
			newStates[lv.Name] = newLevelState(lv, nominal)
		}
	}
	c.levels = newStates
	c.cfg = compiled

	// 更新时刻先让超时失效（左闭），再拒绝新名义席位下永远无法满足的排队者。
	c.advanceExpiryAndPump(op)
	// 热更新是新的调度决策点：旧的宽请求阻塞窗口不再约束更新导致的
	// “立即拒绝”顺序。清空窗口状态，reject 与随后 pump 都从当前激活顺序开始。
	c.resetSchedulingWindows()
	for _, st := range c.levels {
		st.flows.rebuildActivationOrder()
	}
	c.rejectUnsatisfiableOnUpdate(op)
	for _, st := range c.levels {
		st.flows.markQueueDirty()
	}

	// 同一操作内尽可能出队；级别按名称升序，顺序确定可复现。
	names := make([]string, 0, len(c.levels))
	for name := range c.levels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c.pump(c.levels[name], op)
	}
	return op, nil
}

// resetSchedulingWindows 清除每个级别从上一操作延续的调度窗口状态。
func (c *Controller) resetSchedulingWindows() {
	for _, st := range c.levels {
		st.flows.resetWindow()
	}
}

// Snapshot 返回当前占用席位（仅用于测试与可观测）。
func (c *Controller) Snapshot() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int64{}
	for name, st := range c.levels {
		out[name] = st.used
	}
	return out
}

func validateRequest(req *Request) *AdmissionError {
	if req == nil {
		return newError(ClassInvalidArgument, "request is nil")
	}
	if req.ID == "" {
		return newError(ClassInvalidArgument, "request id must be non-empty")
	}
	if req.Seats < 1 || req.Seats > 10 {
		return newError(ClassInvalidArgument, "request seats must be within 1..10")
	}
	if req.User == "" {
		return newError(ClassInvalidArgument, "request user must be non-empty")
	}
	if req.Namespace == "" {
		return newError(ClassInvalidArgument, "request namespace must be non-empty")
	}
	return nil
}

func (c *Controller) classify(req *Request) *compiledRule {
	for i := range c.cfg.rules {
		if c.cfg.rules[i].matches(req) {
			return &c.cfg.rules[i]
		}
	}
	return nil
}

func (c *Controller) flowKey(cr *compiledRule, req *Request) string {
	if cr.distinguish == FlowByNamespace {
		return "ns:" + req.Namespace
	}
	return "user:" + req.User
}

// advanceExpiry 在所有级别弹出已超时请求（左闭），不出队。
// 单纯提交只推进时钟、失效超时；只有完成释放/热更新才在之后 pump。
func (c *Controller) advanceExpiryAndPump(op *OpResult) {
	names := make([]string, 0, len(c.levels))
	for name := range c.levels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := c.levels[name]
		d, ok := st.exp.peekDeadline()
		if !ok || d > c.now {
			continue
		}
		expired := false
		lastServed := st.flows.lastServedNode()
		for _, w := range st.exp.popExpired(c.now) {
			c.expireWaiter(st, w, op)
			expired = true
			if w.flow == lastServed && w.id == st.flows.lastServedHeadID() {
				lastServed = ""
			}
		}
		if expired {
			st.flows.resetWindowAfterExpiry(lastServed)
		}
	}
}

func (c *Controller) expireWaiter(st *levelState, w *waiter, op *OpResult) {
	node := st.flows.flowNode(w.flow)
	if node == nil {
		return
	}
	st.flows.removeWaiter(node, w)
	st.size--
	delete(c.live, w.id)
	op.Events = append(op.Events, Event{
		Kind: "timeout-rejected", ID: w.id, Level: st.name, Flow: w.flow,
		Seats: w.seats, Deadline: w.deadline,
	})
}

// pump 在一个级别内按流轮转尽量出队。轮到的流队首放不下时立即停止，
// 不越过它服务其他请求。
func (c *Controller) pump(st *levelState, op *OpResult) {
	node := st.flows.nextCandidate()
	for node != nil {
		w := node.first
		if w == nil {
			node = node.next
			continue
		}
		if st.used+w.seats > st.nominal {
			st.flows.stopAt(node, w)
			st.flows.pumpEnded(true)
			return
		}
		st.flows.removeHead(node)
		st.exp.remove(w)
		st.size--
		st.used += w.seats
		c.live[w.id].executed = true
		op.Events = append(op.Events, Event{
			Kind: "executed", ID: w.id, Level: st.name, Flow: w.flow,
			Seats: w.seats, Deadline: w.deadline,
		})
		node = st.flows.nextCandidate()
	}
	st.flows.pumpEnded(false)
}

// rejectUnsatisfiableOnUpdate 移除席位超过新名义席位的排队者。
// 仅热更新路径使用（非常态热路径），按轮转顺序/FIFO 确定性扫描。
func (c *Controller) rejectUnsatisfiableOnUpdate(op *OpResult) {
	names := make([]string, 0, len(c.levels))
	for name := range c.levels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := c.levels[name]
		var doomed []*waiter
		st.flows.forEachWaiter(func(w *waiter) {
			if w.seats > st.nominal {
				doomed = append(doomed, w)
			}
		})
		// 热更新拒绝不是常态出队路径，允许线性扫描。
		// 同时拒绝多个请求时按全局入队顺序输出，保证跨运行可复现。
		sort.SliceStable(doomed, func(i, j int) bool { return doomed[i].seq < doomed[j].seq })
		for _, w := range doomed {
			node := st.flows.flowNode(w.flow)
			if node == nil {
				continue
			}
			st.flows.removeWaiter(node, w)
			st.exp.remove(w)
			st.size--
			delete(c.live, w.id)
			op.Events = append(op.Events, Event{
				Kind: "update-rejected", ID: w.id, Level: st.name, Flow: w.flow,
				Seats: w.seats,
			})
		}
	}
}
