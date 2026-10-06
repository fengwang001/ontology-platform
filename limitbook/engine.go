package limitbook

import "sync"

// Engine 是多保单限额账引擎的门面。保单表由引擎级读写锁保护，
// 每张保单内部各有独立的锁：不同保单的操作完全并行，同一保单的
// 并发理赔在保单锁下串行化，结果等价于某个串行顺序。
type Engine struct {
	mu       sync.RWMutex
	policies map[string]*Policy
}

func NewEngine() *Engine {
	return &Engine{policies: make(map[string]*Policy)}
}

// RegisterPolicy 登记保单：承保起始日（非负整数天）、年度长度
// （正整数天）与家庭共享年度限额（非负整数分）。保单号重复按参数非法处理。
func (e *Engine) RegisterPolicy(id string, start, yearLen, familyAnnualLimit int64) error {
	if id == "" || start < 0 || yearLen <= 0 || familyAnnualLimit < 0 {
		return newErr(ErrInvalidParam, "保单号非空、起始日非负、年度长度为正、家庭限额非负")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.policies[id]; ok {
		return newErr(ErrInvalidParam, "保单 %s 已登记", id)
	}
	e.policies[id] = newPolicy(id, start, yearLen, familyAnnualLimit)
	return nil
}

// RegisterMember 登记成员：个人年度限额与个人终身限额（均非负整数分）。
func (e *Engine) RegisterMember(policyID, memberID string, annualLimit, lifetimeLimit int64) error {
	if policyID == "" || memberID == "" || annualLimit < 0 || lifetimeLimit < 0 {
		return newErr(ErrInvalidParam, "编号非空且限额非负")
	}
	p, err := e.policy(policyID)
	if err != nil {
		return err
	}
	return p.addMember(memberID, annualLimit, lifetimeLimit)
}

// RegisterItem 登记赔付项目：个人年度项目限额（非负整数分）。
func (e *Engine) RegisterItem(policyID, itemID string, annualLimit int64) error {
	if policyID == "" || itemID == "" || annualLimit < 0 {
		return newErr(ErrInvalidParam, "编号非空且限额非负")
	}
	p, err := e.policy(policyID)
	if err != nil {
		return err
	}
	return p.addItem(itemID, annualLimit)
}

// EndorseMemberAnnual 批改某成员的个人年度限额，自生效日所在保单年度起生效。
func (e *Engine) EndorseMemberAnnual(policyID, memberID string, effDay, newLimit int64) error {
	if policyID == "" || memberID == "" || effDay < 0 || newLimit < 0 {
		return newErr(ErrInvalidParam, "编号非空、生效日非负、限额非负")
	}
	p, err := e.policy(policyID)
	if err != nil {
		return err
	}
	return p.endorseMemberAnnual(memberID, effDay, newLimit)
}

// EndorseItemAnnual 批改某项目的个人年度项目限额，自生效日所在保单年度起生效。
func (e *Engine) EndorseItemAnnual(policyID, itemID string, effDay, newLimit int64) error {
	if policyID == "" || itemID == "" || effDay < 0 || newLimit < 0 {
		return newErr(ErrInvalidParam, "编号非空、生效日非负、限额非负")
	}
	p, err := e.policy(policyID)
	if err != nil {
		return err
	}
	return p.endorseItemAnnual(itemID, effDay, newLimit)
}

// EndorseFamilyAnnual 批改家庭共享年度限额，自生效日所在保单年度起生效。
func (e *Engine) EndorseFamilyAnnual(policyID string, effDay, newLimit int64) error {
	if policyID == "" || effDay < 0 || newLimit < 0 {
		return newErr(ErrInvalidParam, "保单号非空、生效日非负、限额非负")
	}
	p, err := e.policy(policyID)
	if err != nil {
		return err
	}
	return p.endorseFamilyAnnual(effDay, newLimit)
}

// Settle 结算一笔理赔。任一明细被拒则整笔拒绝，不改动任何账簿。
func (e *Engine) Settle(policyID string, c Claim) (*Settlement, error) {
	if err := validateClaim(c); err != nil {
		return nil, err
	}
	p, err := e.policy(policyID)
	if err != nil {
		return nil, err
	}
	return p.settle(c)
}

// Reverse 冲正一笔已结算理赔，恢复各层剩余额并释放理赔号。
func (e *Engine) Reverse(policyID, claimID string) error {
	if policyID == "" || claimID == "" {
		return newErr(ErrInvalidParam, "保单号与理赔号非空")
	}
	p, err := e.policy(policyID)
	if err != nil {
		return err
	}
	return p.reverse(claimID)
}

func (e *Engine) policy(id string) (*Policy, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.policies[id]
	if !ok {
		return nil, newErr(ErrPolicyNotFound, "保单 %s", id)
	}
	return p, nil
}
