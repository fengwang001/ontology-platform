package apportion

import (
	"fmt"
	"sync"
)

// Engine 分摊赔付引擎。不同被保人的操作可并发进行；
// 同一被保人的操作在该被保人账户锁下串行化。
type Engine struct {
	mu       sync.Mutex
	insureds map[string]*insuredAccount
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{insureds: make(map[string]*insuredAccount)}
}

// RegisterPolicy 登记保单。
func (e *Engine) RegisterPolicy(p Policy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	acc, _ := e.account(p.InsuredID, true)
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if _, ok := acc.policies[p.PolicyNo]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicatePolicy, p.PolicyNo)
	}
	acc.addPolicy(p)
	return nil
}

// AcceptLoss 受理一笔损失并立即裁定分摊、扣减年度累计限额。
func (e *Engine) AcceptLoss(l Loss) (Result, error) {
	if err := validateLoss(l); err != nil {
		return Result{}, err
	}
	acc, ok := e.account(l.InsuredID, false)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrInsuredNotFound, l.InsuredID)
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if _, dup := acc.lossIDs[l.LossID]; dup {
		return Result{}, fmt.Errorf("%w: %s", ErrLossExists, l.LossID)
	}
	res := Result{LossID: l.LossID}
	var involved []*policyState
	for _, no := range acc.order {
		ps := acc.policies[no]
		if ps.remaining > 0 && ps.covers(l.LossDay) {
			involved = append(involved, ps)
		}
	}
	if len(involved) == 0 {
		res.Verdict = VerdictNoPayablePolicy
	} else {
		res.Verdict = VerdictApportioned
		res.Payouts = apportion(involved, l.Amount)
		for _, po := range res.Payouts {
			acc.policies[po.PolicyNo].remaining -= po.Amount
		}
	}
	acc.losses = append(acc.losses, lossRecord{loss: l, payouts: res.Payouts})
	acc.lossIDs[l.LossID] = struct{}{}
	return res, nil
}

// CancelLoss 撤销该被保人名下受理次序最后的一笔损失。
func (e *Engine) CancelLoss(insuredID, lossID string) error {
	if insuredID == "" || lossID == "" {
		return fmt.Errorf("%w: 被保人标识与损失号不能为空", ErrInvalidParam)
	}
	acc, ok := e.account(insuredID, false)
	if !ok {
		return fmt.Errorf("%w: %s", ErrInsuredNotFound, insuredID)
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if _, ok := acc.lossIDs[lossID]; !ok {
		return fmt.Errorf("%w: %s", ErrLossNotFound, lossID)
	}
	last := acc.losses[len(acc.losses)-1]
	if last.loss.LossID != lossID {
		return fmt.Errorf("%w: %s", ErrNotLastLoss, lossID)
	}
	for _, po := range last.payouts {
		acc.policies[po.PolicyNo].remaining += po.Amount
	}
	acc.losses = acc.losses[:len(acc.losses)-1]
	delete(acc.lossIDs, lossID)
	return nil
}

// Remaining 查询保单年度累计限额剩余。
func (e *Engine) Remaining(insuredID, policyNo string) (int64, bool) {
	acc, ok := e.account(insuredID, false)
	if !ok {
		return 0, false
	}
	acc.mu.Lock()
	defer acc.mu.Unlock()
	ps, ok := acc.policies[policyNo]
	if !ok {
		return 0, false
	}
	return ps.remaining, true
}

// account 按被保人标识取账户；create 为真时不存在则创建。
func (e *Engine) account(insuredID string, create bool) (*insuredAccount, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	acc, ok := e.insureds[insuredID]
	if !ok && create {
		acc = newInsuredAccount()
		e.insureds[insuredID] = acc
	}
	return acc, ok || create
}

func validatePolicy(p Policy) error {
	switch {
	case p.InsuredID == "" || p.PolicyNo == "":
		return fmt.Errorf("%w: 被保人标识与保单编号不能为空", ErrInvalidParam)
	case p.DeductiblePerLoss < 0:
		return fmt.Errorf("%w: 免赔额不能为负", ErrInvalidParam)
	case p.LimitPerLoss <= 0 || p.AnnualLimit <= 0:
		return fmt.Errorf("%w: 限额必须为正整数分", ErrInvalidParam)
	case p.StartDay >= p.EndDay:
		return fmt.Errorf("%w: 承保区间左端必须小于右端", ErrInvalidParam)
	case !p.Clause.Valid():
		return fmt.Errorf("%w: 未知条款类型 %d", ErrInvalidParam, int(p.Clause))
	}
	return nil
}

func validateLoss(l Loss) error {
	switch {
	case l.LossID == "" || l.InsuredID == "":
		return fmt.Errorf("%w: 损失号与被保人标识不能为空", ErrInvalidParam)
	case l.Amount <= 0:
		return fmt.Errorf("%w: 损失金额必须为正整数分", ErrInvalidParam)
	}
	return nil
}
