package claims

import (
	"container/heap"
	"sync"
)

// reviewKind 复核形式：单人或双人。
type reviewKind int

const (
	kindSingle reviewKind = iota + 1
	kindDouble
)

// slot 复核空位。结论为空位被分配后自分配时刻起有时限。
type slot struct {
	assigned   bool
	assignee   string
	assignedAt int64
	conclusion Conclusion
	auto       bool
}

// expired 报告空位在当前时刻是否已超时（到期时刻即视为超时）。
func (s *slot) expired(now, timeout int64) bool {
	return s.assigned && s.conclusion == ConclusionNone && now >= s.assignedAt+timeout
}

// caseFile 案件档案，总分在受理时刻固化。
type caseFile struct {
	id         string
	acceptTime int64
	branch     string
	amount     int64
	score      int
	kind       reviewKind
	slots      []*slot
	outcome    Outcome
}

func (c *caseFile) final() bool { return c.outcome != OutcomeNone }

// pendingItem 待分配堆元素，按 (受理时刻, 入队序号) 排序。
type pendingItem struct {
	caseID     string
	acceptTime int64
	seq        uint64
	index      int
}

// pendingHeap 索引堆：RequestAssign 只读堆顶，开销与已分配/已终态案件数无关。
type pendingHeap []*pendingItem

func (h pendingHeap) Len() int { return len(h) }

func (h pendingHeap) Less(i, j int) bool {
	if h[i].acceptTime != h[j].acceptTime {
		return h[i].acceptTime < h[j].acceptTime
	}
	return h[i].seq < h[j].seq
}

func (h pendingHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *pendingHeap) Push(x any) {
	item := x.(*pendingItem)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *pendingHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}

// Engine 理赔反欺诈评分与人工复核工作流引擎。
// 所有入口以互斥锁串行化，并发调用等价于某个串行顺序。
type Engine struct {
	mu        sync.Mutex
	cfg       Config
	now       int64
	rules     map[string]Rule
	cases     map[string]*caseFile
	reviewers map[string]Reviewer
	pending   pendingHeap
	queued    map[string]*pendingItem
	seq       uint64
}

// NewEngine 构造引擎；配置非法（低阈值不小于高阈值或时限非正）报“参数非法”。
func NewEngine(cfg Config) (*Engine, error) {
	if !cfg.valid() {
		return nil, ErrInvalidParam
	}
	return &Engine{
		cfg:       cfg,
		rules:     make(map[string]Rule),
		cases:     make(map[string]*caseFile),
		reviewers: make(map[string]Reviewer),
		queued:    make(map[string]*pendingItem),
	}, nil
}

// SetRule 新增或覆盖一条指标规则。规则库变更不影响已受理案件的固化分值。
func (e *Engine) SetRule(ruleID, feature string, score int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ruleID == "" || feature == "" {
		return ErrInvalidParam
	}
	e.rules[ruleID] = Rule{ID: ruleID, Feature: feature, Score: score}
	return nil
}

// RemoveRule 移除一条指标规则；规则不存在时为空操作。
func (e *Engine) RemoveRule(ruleID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ruleID == "" {
		return ErrInvalidParam
	}
	delete(e.rules, ruleID)
	return nil
}

// RegisterReviewer 登记复核员。
func (e *Engine) RegisterReviewer(id, branch string, level Level) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || branch == "" || !level.valid() {
		return ErrInvalidParam
	}
	if _, dup := e.reviewers[id]; dup {
		return ErrInvalidParam
	}
	e.reviewers[id] = Reviewer{ID: id, Branch: branch, Level: level}
	return nil
}

// AcceptCase 受理案件：按当时生效的规则库评分并固化，随后按阈值分流。
// 受理时刻不得早于当前时刻，否则报“时钟回退”；受理后当前时刻推进到受理时刻。
func (e *Engine) AcceptCase(id string, acceptTime int64, features []string, branch string, amount int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || branch == "" || acceptTime < 0 || amount < 0 {
		return ErrInvalidParam
	}
	if _, dup := e.cases[id]; dup {
		return ErrInvalidParam
	}
	if acceptTime < e.now {
		return ErrClockRollback
	}
	e.now = acceptTime
	feat := make(map[string]struct{}, len(features))
	for _, f := range features {
		feat[f] = struct{}{}
	}
	score := 0
	for _, r := range e.rules {
		if _, ok := feat[r.Feature]; ok {
			score += r.Score
		}
	}
	if score < 0 {
		score = 0
	}
	c := &caseFile{id: id, acceptTime: acceptTime, branch: branch, amount: amount, score: score}
	switch {
	case score < e.cfg.LowThreshold:
		// 严格小于低阈值：自动通过并进入终态；任何总分都不直接自动拒付。
		c.outcome = OutcomePass
	case score >= e.cfg.HighThreshold:
		c.kind = kindDouble
		c.slots = []*slot{{}, {}}
		e.enqueueLocked(c)
	default:
		c.kind = kindSingle
		c.slots = []*slot{{}}
		e.enqueueLocked(c)
	}
	e.cases[id] = c
	return nil
}

// Advance 推进当前时刻；回退报“时钟回退”。
// 时限采用惰性结算：到期空位在案件被触碰时按“通过”自动填入。
func (e *Engine) Advance(now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return ErrClockRollback
	}
	e.now = now
	return nil
}

// Withdraw 被保人撤回案件；撤回为终态且与通过、拒付可区分。
func (e *Engine) Withdraw(caseID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if caseID == "" {
		return ErrInvalidParam
	}
	c, ok := e.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	e.sweepCaseLocked(c)
	if c.final() {
		return ErrCaseFinal
	}
	c.outcome = OutcomeWithdrawn
	e.dequeueLocked(caseID)
	return nil
}

// Snapshot 返回案件只读快照；读取前惰性结算该案件已到期空位。
func (e *Engine) Snapshot(caseID string) (CaseSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if caseID == "" {
		return CaseSnapshot{}, ErrInvalidParam
	}
	c, ok := e.cases[caseID]
	if !ok {
		return CaseSnapshot{}, ErrCaseNotFound
	}
	e.sweepCaseLocked(c)
	snap := CaseSnapshot{ID: c.id, Score: c.score, Outcome: c.outcome}
	for _, s := range c.slots {
		snap.Slots = append(snap.Slots, SlotSnapshot{
			Assigned:   s.assigned,
			Assignee:   s.assignee,
			Conclusion: s.conclusion,
			Auto:       s.auto,
		})
	}
	return snap, nil
}

// Now 返回当前时刻。
func (e *Engine) Now() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// PendingLen 返回待分配案件数（仅供测试与性能验证）。
func (e *Engine) PendingLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}

// enqueueLocked 将存在未分配空位的案件放入待分配堆。
func (e *Engine) enqueueLocked(c *caseFile) {
	if _, ok := e.queued[c.id]; ok {
		return
	}
	item := &pendingItem{caseID: c.id, acceptTime: c.acceptTime, seq: e.seq}
	e.seq++
	heap.Push(&e.pending, item)
	e.queued[c.id] = item
}

// dequeueLocked 将案件移出待分配堆（空位全部分配完毕或进入终态）。
func (e *Engine) dequeueLocked(caseID string) {
	item, ok := e.queued[caseID]
	if !ok {
		return
	}
	heap.Remove(&e.pending, item.index)
	delete(e.queued, caseID)
}

// sweepCaseLocked 惰性结算该案件所有已到期空位：按“通过”自动填入，
// 视同该空位复核员提交了通过。
func (e *Engine) sweepCaseLocked(c *caseFile) {
	if c.final() {
		return
	}
	for i := 0; i < len(c.slots); i++ {
		if c.slots[i].expired(e.now, e.cfg.ReviewTimeout) {
			e.recordConclusionLocked(c, i, ConclusionPass, true)
		}
	}
}

// recordConclusionLocked 记录空位结论并推进案件状态：
// 单人复核以该结论为案件结论；双人复核一致则为案件结论，
// 不一致则追加仲裁空位并重新进入待分配；仲裁结论即案件结论。
func (e *Engine) recordConclusionLocked(c *caseFile, idx int, concl Conclusion, auto bool) {
	s := c.slots[idx]
	s.conclusion = concl
	s.auto = auto
	switch {
	case c.kind == kindSingle:
		c.outcome = outcomeOf(concl)
		e.dequeueLocked(c.id)
	case idx == len(c.slots)-1 && len(c.slots) == 3:
		c.outcome = outcomeOf(concl)
		e.dequeueLocked(c.id)
	default:
		first, second := c.slots[0], c.slots[1]
		if first.conclusion == ConclusionNone || second.conclusion == ConclusionNone {
			return
		}
		if first.conclusion == second.conclusion {
			c.outcome = outcomeOf(first.conclusion)
			e.dequeueLocked(c.id)
			return
		}
		c.slots = append(c.slots, &slot{})
		e.enqueueLocked(c)
	}
}

func outcomeOf(concl Conclusion) Outcome {
	if concl == ConclusionReject {
		return OutcomeReject
	}
	return OutcomePass
}
