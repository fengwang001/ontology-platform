package ontology

import (
	"fmt"
	"sync"
)

// slot 是一个复核空位。
type slot struct {
	reviewerID string
	assigned   bool
	done       bool
	auto       bool // 超时自动填入「通过」
	void       bool // 撤回后作废
	verdict    Verdict
	assignTime int64
}

// caseRecord 是案件的内部表示，评分在受理时固化。
type caseRecord struct {
	id         string
	acceptTime int64
	seq        int64
	branch     string
	amount     int64
	score      int
	state      CaseState
	slots      []*slot
	heapIndex  int
}

// reviewer 是已登记复核员。
type reviewer struct {
	id     string
	branch string
	level  Level
}

// Engine 是理赔反欺诈工作流引擎，所有入口可并发调用，
// 内部以单互斥锁串行化，结果等价于某个串行顺序。
type Engine struct {
	mu        sync.Mutex
	low       int
	high      int
	timeout   int64
	now       int64
	seq       int64
	rules     map[string]Rule
	reviewers map[string]*reviewer
	cases     map[string]*caseRecord
	order     []*caseRecord
	queue     waitHeap
}

// NewEngine 构造引擎。要求 0 <= 低阈值 < 高阈值，空位时限为正整数秒。
func NewEngine(lowThreshold, highThreshold int, slotTimeout int64) (*Engine, error) {
	if lowThreshold < 0 || highThreshold <= lowThreshold || slotTimeout <= 0 {
		return nil, fmt.Errorf("%w: 需满足 0<=低阈值<高阈值 且时限为正", ErrInvalidParam)
	}
	return &Engine{
		low:       lowThreshold,
		high:      highThreshold,
		timeout:   slotTimeout,
		rules:     make(map[string]Rule),
		reviewers: make(map[string]*reviewer),
		cases:     make(map[string]*caseRecord),
	}, nil
}

// UpsertRule 新增或更新一条指标规则，只影响之后受理的案件。
func (e *Engine) UpsertRule(rule Rule) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if rule.ID == "" || rule.Code == "" {
		return fmt.Errorf("%w: 规则编号与特征编码不能为空", ErrInvalidParam)
	}
	e.rules[rule.ID] = rule
	return nil
}

// DeleteRule 删除一条指标规则，只影响之后受理的案件。
func (e *Engine) DeleteRule(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return fmt.Errorf("%w: 规则编号不能为空", ErrInvalidParam)
	}
	if _, ok := e.rules[id]; !ok {
		return fmt.Errorf("%w: 规则不存在 %q", ErrInvalidParam, id)
	}
	delete(e.rules, id)
	return nil
}

// RegisterReviewer 登记复核员，级别必须为一级或二级。
func (e *Engine) RegisterReviewer(id, branch string, level Level) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || (level != LevelOne && level != LevelTwo) {
		return fmt.Errorf("%w: 复核员编号为空或级别非法", ErrInvalidParam)
	}
	if _, ok := e.reviewers[id]; ok {
		return fmt.Errorf("%w: 复核员重复 %q", ErrInvalidParam, id)
	}
	e.reviewers[id] = &reviewer{id: id, branch: branch, level: level}
	return nil
}

// Accept 受理案件：按下当时生效的规则库计算总分并固化到案件上，
// 总分下限为零；随后按阈值分流，任何总分都不会直接自动拒付。
func (e *Engine) Accept(id string, acceptTime int64, features []string, branch string, amount int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || acceptTime < 0 || amount < 0 {
		return fmt.Errorf("%w: 案件号为空或时刻/金额为负", ErrInvalidParam)
	}
	if _, ok := e.cases[id]; ok {
		return fmt.Errorf("%w: 案件号重复 %q", ErrInvalidParam, id)
	}
	feat := make(map[string]struct{}, len(features))
	for _, f := range features {
		feat[f] = struct{}{}
	}
	score := 0
	for _, r := range e.rules {
		if _, ok := feat[r.Code]; ok {
			score += r.Score
		}
	}
	if score < 0 {
		score = 0
	}
	e.seq++
	c := &caseRecord{
		id:         id,
		acceptTime: acceptTime,
		seq:        e.seq,
		branch:     branch,
		amount:     amount,
		score:      score,
		heapIndex:  -1,
	}
	switch {
	case score < e.low:
		c.state = StateAutoPassed
	case score >= e.high:
		c.state = StateWaiting
		c.slots = []*slot{{}, {}}
		e.queue.push(c)
	default:
		c.state = StateWaiting
		c.slots = []*slot{{}}
		e.queue.push(c)
	}
	e.cases[id] = c
	e.order = append(e.order, c)
	return nil
}

// Advance 把当前时刻推进到 now（只能前进），并把时限到期的空位
// 自动填入「通过」，视同该空位复核员提交了通过。
func (e *Engine) Advance(now int64) error {
	if now < 0 {
		return fmt.Errorf("%w: 时刻为负", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return fmt.Errorf("%w: 当前 %d 目标 %d", ErrClockRewind, e.now, now)
	}
	e.now = now
	for _, c := range e.order {
		e.materialize(c)
	}
	return nil
}

// materialize 把案件中已到期的空位自动填入「通过」并收敛状态机，
// 返回本次被自动填入的复核员编号。
func (e *Engine) materialize(c *caseRecord) []string {
	if c.state.Terminal() {
		return nil
	}
	var filled []string
	for _, s := range c.slots {
		if s.assigned && !s.done && e.now >= s.assignTime+e.timeout {
			s.done = true
			s.auto = true
			s.verdict = VerdictPass
			filled = append(filled, s.reviewerID)
		}
	}
	if len(filled) > 0 {
		e.resolve(c)
	}
	return filled
}

// resolve 在空位结论齐备后收敛案件状态：单人复核以该空位结论为准；
// 双人复核一致则以共同结论为准，不一致则开启仲裁空位；
// 仲裁结论即案件结论。
func (e *Engine) resolve(c *caseRecord) {
	if c.state.Terminal() {
		return
	}
	switch len(c.slots) {
	case 1:
		if c.slots[0].done {
			e.finish(c, c.slots[0].verdict)
		}
	case 2:
		if c.slots[0].done && c.slots[1].done {
			if c.slots[0].verdict == c.slots[1].verdict {
				e.finish(c, c.slots[0].verdict)
			} else {
				c.slots = append(c.slots, &slot{})
				c.state = StateWaiting
				e.queue.push(c)
			}
		}
	case 3:
		if c.slots[2].done {
			e.finish(c, c.slots[2].verdict)
		}
	}
}

func (e *Engine) finish(c *caseRecord, v Verdict) {
	if v == VerdictReject {
		c.state = StateRejected
	} else {
		c.state = StatePassed
	}
	e.queue.remove(c)
}

// SlotSnapshot 是空位的只读快照。
type SlotSnapshot struct {
	Assigned   bool
	ReviewerID string
	Done       bool
	Auto       bool
	Void       bool
	Verdict    Verdict
	AssignTime int64
}

// CaseSnapshot 是案件的只读快照。
type CaseSnapshot struct {
	ID         string
	Score      int
	State      CaseState
	AcceptTime int64
	Slots      []SlotSnapshot
}

// Snapshot 返回案件快照，第二个返回值报告案件是否存在。
func (e *Engine) Snapshot(caseID string) (CaseSnapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cases[caseID]
	if !ok {
		return CaseSnapshot{}, false
	}
	e.materialize(c)
	snap := CaseSnapshot{ID: c.id, Score: c.score, State: c.state, AcceptTime: c.acceptTime}
	for _, s := range c.slots {
		snap.Slots = append(snap.Slots, SlotSnapshot{
			Assigned:   s.assigned,
			ReviewerID: s.reviewerID,
			Done:       s.done,
			Auto:       s.auto,
			Void:       s.void,
			Verdict:    s.verdict,
			AssignTime: s.assignTime,
		})
	}
	return snap, true
}

// PendingCount 返回等待分配的案件数（堆中只含等待中的案件）。
func (e *Engine) PendingCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.queue.Len()
}

// Now 返回当前时刻。
func (e *Engine) Now() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}
