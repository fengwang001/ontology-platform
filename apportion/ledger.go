package apportion

import (
	"slices"
	"sync"
)

// policyState 保单登记信息及其限额账。
type policyState struct {
	policy    Policy
	remaining int64 // 年度累计限额剩余（分）
}

// covers 报告承保区间 [StartDay, EndDay) 是否覆盖损失日。
func (p *policyState) covers(day int) bool {
	return p.policy.StartDay <= day && day < p.policy.EndDay
}

// independentLiability 独立责任额：假设只有本保单时按免赔与限额应赔的金额，
// 且不超过年度累计剩余额。
func (p *policyState) independentLiability(amount int64) int64 {
	liab := amount - p.policy.DeductiblePerLoss
	if liab < 0 {
		liab = 0
	}
	if liab > p.policy.LimitPerLoss {
		liab = p.policy.LimitPerLoss
	}
	if liab > p.remaining {
		liab = p.remaining
	}
	return liab
}

// weight 限额比例型分摊权重：每次损失限额与年度累计剩余两者较小者。
func (p *policyState) weight() int64 {
	if p.policy.LimitPerLoss < p.remaining {
		return p.policy.LimitPerLoss
	}
	return p.remaining
}

// lossRecord 一笔已受理损失的留痕，用于末笔撤销时恢复限额账。
type lossRecord struct {
	loss    Loss
	payouts []Payout
}

// insuredAccount 单个被保人的全部状态；同一被保人的操作在此串行化。
type insuredAccount struct {
	mu       sync.Mutex
	policies map[string]*policyState // 保单编号 -> 保单账
	order    []string                // 保单编号字典序，保证分摊遍历确定
	losses   []lossRecord            // 受理次序栈，仅末笔可撤销
	lossIDs  map[string]struct{}     // 已受理损失号集合
}

func newInsuredAccount() *insuredAccount {
	return &insuredAccount{
		policies: make(map[string]*policyState),
		lossIDs:  make(map[string]struct{}),
	}
}

// addPolicy 登记保单并保持 order 字典序。
func (a *insuredAccount) addPolicy(p Policy) {
	a.policies[p.PolicyNo] = &policyState{policy: p, remaining: p.AnnualLimit}
	i, _ := slices.BinarySearch(a.order, p.PolicyNo)
	a.order = slices.Insert(a.order, i, p.PolicyNo)
}
