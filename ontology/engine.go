package ontology

import "sync"

// Engine 是多保单限额账引擎的入口。保单之间可并行，
// 同一保单内的操作在保单级互斥锁下串行化。
type Engine struct {
	mu       sync.RWMutex
	policies map[string]*Policy
}

func NewEngine() *Engine {
	return &Engine{policies: make(map[string]*Policy)}
}

func (e *Engine) getPolicy(id string) *Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policies[id]
}

// RegisterPolicy 登记保单及其成员与项目。
func (e *Engine) RegisterPolicy(in PolicyInput) error {
	p, perr := newPolicy(in)
	if perr != nil {
		return perr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.policies[in.ID]; dup {
		return errOf(ErrInvalidParam, in.ID, "保单已登记")
	}
	e.policies[in.ID] = p
	return nil
}

// SettleClaim 结算一笔理赔，返回与输入明细一一对应的赔付额。
// 任一校验失败则整笔拒绝，不改变任何状态。
func (e *Engine) SettleClaim(policyID string, in ClaimInput) ([]int64, error) {
	if err := validateClaim(policyID, in); err != nil {
		return nil, err
	}
	p := e.getPolicy(policyID)
	if p == nil {
		return nil, errOf(ErrPolicyNotFound, policyID, "")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	payouts, perr := p.settle(in)
	if perr != nil {
		return nil, perr
	}
	return payouts, nil
}

func validateClaim(policyID string, in ClaimInput) *Error {
	if in.ID == "" || in.MemberID == "" || len(in.Lines) == 0 {
		return errOf(ErrInvalidParam, policyID, "理赔参数非法")
	}
	for _, ln := range in.Lines {
		if ln.ItemID == "" || ln.Day < 0 || ln.Amount <= 0 {
			return errOf(ErrInvalidParam, policyID, "明细参数非法")
		}
	}
	return nil
}

// ReverseClaim 冲正一笔已结算理赔；仅允许冲正该成员名下受理次序最后的一笔。
func (e *Engine) ReverseClaim(policyID, claimID string) error {
	if claimID == "" {
		return errOf(ErrInvalidParam, policyID, "理赔号为空")
	}
	p := e.getPolicy(policyID)
	if p == nil {
		return errOf(ErrPolicyNotFound, policyID, "")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if perr := p.reverse(claimID); perr != nil {
		return perr
	}
	return nil
}

// Endorse 在指定生效日批改某层限额，对生效日所在保单年度及之后年度生效。
// objectID 在 EndorseFamilyAnnual 下忽略。
func (e *Engine) Endorse(policyID string, kind EndorseKind, objectID string, effectiveDay, newLimit int64) error {
	if effectiveDay < 0 || newLimit < 0 {
		return errOf(ErrInvalidParam, policyID, "批改参数非法")
	}
	if kind != EndorseMemberAnnual && kind != EndorseItemAnnual && kind != EndorseFamilyAnnual {
		return errOf(ErrInvalidParam, policyID, "未知批改类型")
	}
	if kind != EndorseFamilyAnnual && objectID == "" {
		return errOf(ErrInvalidParam, policyID, "批改对象为空")
	}
	p := e.getPolicy(policyID)
	if p == nil {
		return errOf(ErrPolicyNotFound, policyID, "")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if perr := p.endorse(kind, objectID, effectiveDay, newLimit); perr != nil {
		return perr
	}
	return nil
}

// Snapshot 返回指定成员、项目、年度的各层剩余额快照。
func (e *Engine) Snapshot(policyID, memberID, itemID string, year int64) (Snapshot, error) {
	p := e.getPolicy(policyID)
	if p == nil {
		return Snapshot{}, errOf(ErrPolicyNotFound, policyID, "")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	snap, perr := p.snapshot(memberID, itemID, year)
	if perr != nil {
		return Snapshot{}, perr
	}
	return snap, nil
}

// WorkUnits 返回该保单累计工作量计数（仅随明细条数增长），用于性能验证。
func (e *Engine) WorkUnits(policyID string) int64 {
	p := e.getPolicy(policyID)
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ops
}
