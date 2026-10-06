// Package ledger 记录每个运单最近一次计价结果与结算固化结果。
// 结算金额为计价明细快照，此后合同集合任何变化都不再触及它。
package ledger

import "ontology/freight/model"

type entry struct {
	latest  *model.FeeBreakdown // 最近一次计价（未结算时随再次计价更新）
	settled *model.FeeBreakdown // 非空即已结算，金额固化
}

// Ledger 运单计价/结算台账。
type Ledger struct{ m map[string]*entry }

// New 创建空台账。
func New() *Ledger { return &Ledger{m: map[string]*entry{}} }

// SavePricing 记录一次计价结果；已结算运单的固化金额不变。
func (l *Ledger) SavePricing(fb *model.FeeBreakdown) {
	e := l.m[fb.WaybillNumber]
	if e == nil {
		e = &entry{}
		l.m[fb.WaybillNumber] = e
	}
	e.latest = cloneBreakdown(fb)
	// 刻意不触碰 e.settled：结算后再次计价只更新“最近报价”，固化金额原封不动。
}

// Settle 对运单结算：未计价或已结算分别返回错误。
func (l *Ledger) Settle(waybillNumber string) (*model.FeeBreakdown, error) {
	e := l.m[waybillNumber]
	if e == nil || e.latest == nil {
		return nil, model.NewError(model.CodeInvalidArgument, "运单尚未计价，无法结算: %s", waybillNumber)
	}
	if e.settled != nil {
		return nil, model.NewError(model.CodeAlreadySettled, "运单已结算: %s", waybillNumber)
	}
	e.settled = cloneBreakdown(e.latest)
	return cloneBreakdown(e.settled), nil
}

// Settlement 返回已结算明细；未结算返回 nil。
func (l *Ledger) Settlement(waybillNumber string) *model.FeeBreakdown {
	if e := l.m[waybillNumber]; e != nil && e.settled != nil {
		return cloneBreakdown(e.settled)
	}
	return nil
}

// Latest 返回最近计价明细；从未计价返回 nil。
func (l *Ledger) Latest(waybillNumber string) *model.FeeBreakdown {
	if e := l.m[waybillNumber]; e != nil && e.latest != nil {
		return cloneBreakdown(e.latest)
	}
	return nil
}

// cloneBreakdown 深拷贝明细（含阶梯行），杜绝外部修改污染台账。
func cloneBreakdown(fb *model.FeeBreakdown) *model.FeeBreakdown {
	cp := *fb
	cp.TierLines = append([]model.TierLine(nil), fb.TierLines...)
	return &cp
}
