package medclaim

import (
	"strconv"
	"sync"
)

// claimRecord 已结算理赔记录及其结算前快照，供撤销回滚。
type claimRecord struct {
	policyID  string
	claimID   string
	yearIndex int
	seq       int // 全局受理序号
	preDed    int // 结算前年度已扣免赔
	preOOP    int // 结算前年度自付累计
	claim     *Claim
	result    *SettlementResult
}

// policyState 单张保单的全部运行态。
type policyState struct {
	policy *Policy
	accs   map[int]*yearAcc // 按保单年度序号索引
	claims map[string]*claimRecord
	order  []string // 结算受理次序（claimID）
}

// Engine 理赔分摊结算引擎，支持并发提交与撤销。
type Engine struct {
	mu       sync.Mutex
	policies map[string]*policyState
	seq      int
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{policies: map[string]*policyState{}}
}

// RegisterPolicy 登记保单条款。重复登记同一保单号报参数非法。
func (e *Engine) RegisterPolicy(p *Policy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.policies[p.PolicyID]; ok {
		return newError(ErrInvalidParameter, "保单已存在: "+p.PolicyID)
	}
	e.policies[p.PolicyID] = &policyState{
		policy: clonePolicy(p),
		accs:   map[int]*yearAcc{},
		claims: map[string]*claimRecord{},
	}
	return nil
}

// Submit 提交并结算一笔理赔。
// 拒绝次序：参数非法 > 保单不存在 > 理赔已存在 > 事故日未承保。
// 被拒绝的操作不改变任何累计值与理赔记录。
func (e *Engine) Submit(c *Claim) (*SettlementResult, error) {
	if err := validateClaim(c); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	s, ok := e.policies[c.PolicyID]
	if !ok {
		return nil, newError(ErrPolicyNotFound, "保单不存在: "+c.PolicyID)
	}
	if _, ok := s.claims[c.ClaimID]; ok {
		return nil, newError(ErrClaimExists, "理赔已存在: "+c.ClaimID)
	}
	yi := policyYear(s.policy, c.EventDay)
	if yi < 0 {
		return nil, newError(ErrDateNotCovered, "事故日未承保: 第"+strconv.Itoa(c.EventDay)+"天")
	}

	acc := s.accs[yi]
	if acc == nil {
		acc = &yearAcc{yearIndex: yi}
		s.accs[yi] = acc
	}
	preDed, preOOP := acc.deductibleUsed, acc.oopTotal
	res := settle(s.policy, cloneClaim(c), acc)

	e.seq++
	s.claims[c.ClaimID] = &claimRecord{
		policyID:  c.PolicyID,
		claimID:   c.ClaimID,
		yearIndex: yi,
		seq:       e.seq,
		preDed:    preDed,
		preOOP:    preOOP,
		claim:     cloneClaim(c),
		result:    res,
	}
	s.order = append(s.order, c.ClaimID)
	return res, nil
}

// Cancel 撤销指定理赔（须为所属年度结算次序最后一笔）。
// 拒绝次序：保单不存在 > 理赔不存在 > 非末笔。
// 撤销后年度累计恢复到该笔结算前，理赔号释放。
func (e *Engine) Cancel(policyID, claimID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	s, ok := e.policies[policyID]
	if !ok {
		return newError(ErrPolicyNotFound, "保单不存在: "+policyID)
	}
	rec, ok := s.claims[claimID]
	if !ok {
		return newError(ErrClaimNotFound, "理赔不存在: "+claimID)
	}
	if lastID, ok := s.lastInYear(rec.yearIndex); !ok || lastID != claimID {
		return newError(ErrNotLast, "非末笔: "+claimID)
	}

	acc := s.accs[rec.yearIndex]
	if acc == nil {
		acc = &yearAcc{yearIndex: rec.yearIndex}
		s.accs[rec.yearIndex] = acc
	}
	acc.deductibleUsed = rec.preDed
	acc.oopTotal = rec.preOOP
	if rec.preDed == 0 && rec.preOOP == 0 {
		// 该笔是其年度第一笔且回到零值：年度恢复为"从未发生"。
		delete(s.accs, rec.yearIndex)
	}
	delete(s.claims, claimID)
	idx := -1
	for i := len(s.order) - 1; i >= 0; i-- {
		if s.order[i] == claimID {
			idx = i
			break
		}
	}
	s.order = append(s.order[:idx], s.order[idx+1:]...)
	return nil
}

// YearTotals 返回某保单某事故日所属年度的已扣免赔与自付累计（便于验证）。
func (e *Engine) YearTotals(policyID string, day int) (deductible, oop int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.policies[policyID]
	if !ok {
		return 0, 0, newError(ErrPolicyNotFound, "保单不存在: "+policyID)
	}
	yi := policyYear(s.policy, day)
	if yi < 0 {
		return 0, 0, newError(ErrDateNotCovered, "事故日未承保")
	}
	if acc := s.accs[yi]; acc != nil {
		return acc.deductibleUsed, acc.oopTotal, nil
	}
	return 0, 0, nil
}

// lastInYear 返回指定年度结算受理次序最后的理赔号。
// order 为追加式受理序列，从尾部反向扫描通常为 O(1)。
func (s *policyState) lastInYear(yi int) (string, bool) {
	for i := len(s.order) - 1; i >= 0; i-- {
		id := s.order[i]
		if r := s.claims[id]; r != nil && r.yearIndex == yi {
			return id, true
		}
	}
	return "", false
}

func clonePolicy(p *Policy) *Policy {
	cp := *p
	if p.ExcludedCodes != nil {
		cp.ExcludedCodes = make(map[string]struct{}, len(p.ExcludedCodes))
		for k := range p.ExcludedCodes {
			cp.ExcludedCodes[k] = struct{}{}
		}
	}
	return &cp
}

func cloneClaim(c *Claim) *Claim {
	cp := *c
	cp.Items = append([]Item(nil), c.Items...)
	return &cp
}
