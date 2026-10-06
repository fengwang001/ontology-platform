package ontology

// policyState 为单张保单的限额账（仅账户模块内部可见）。
type policyState struct {
	spec    Policy
	remains int64 // 年度累计剩余额
}

// insuredBook 为单个被保人的保单账与损失受理记录。
type insuredBook struct {
	policies map[string]*policyState
	order    []string // 保单登记顺序
	losses   []*acceptedLoss
	lossSet  map[string]int
}

type acceptedLoss struct {
	lossID  string
	day     int
	amount  int64
	pays    map[string]int64 // 受理时各保单应赔额（分）；无可赔保单时为空
	noPayer bool             // 以「无可赔保单」结论受理
}

func newBook() *insuredBook {
	return &insuredBook{
		policies: map[string]*policyState{},
		lossSet:  map[string]int{},
	}
}

func (b *insuredBook) add(p Policy) {
	b.policies[p.PolicyID] = &policyState{spec: p, remains: p.AnnualLimit}
	b.order = append(b.order, p.PolicyID)
}

func (b *insuredBook) balance(policyID string) (int64, bool) {
	st, ok := b.policies[policyID]
	if !ok {
		return 0, false
	}
	return st.remains, true
}

// covering 返回承保区间覆盖损失日、且年度累计仍有剩余的保单，按保单编号字典序排列，
// 使分摊的全部 tie-break 与迭代结果可精确复现。
func (b *insuredBook) covering(day int) []*policyState {
	out := make([]*policyState, 0, len(b.order))
	for _, id := range b.order {
		st := b.policies[id]
		if st.remains > 0 && day >= st.spec.CoverFrom && day < st.spec.CoverTo {
			out = append(out, st)
		}
	}
	return out
}

// apply 扣减限额账并记录受理结果，返回可用于撤销的记录。
func (b *insuredBook) apply(l Loss, pays map[string]int64, noPayer bool) *acceptedLoss {
	for id, v := range pays {
		b.policies[id].remains -= v
	}
	rec := &acceptedLoss{lossID: l.LossID, day: l.Day, amount: l.Amount, pays: pays, noPayer: noPayer}
	b.lossSet[l.LossID] = len(b.losses)
	b.losses = append(b.losses, rec)
	return rec
}

// undoLast 撤销受理次序最后的损失并恢复限额账。
func (b *insuredBook) undoLast() {
	n := len(b.losses)
	rec := b.losses[n-1]
	for id, v := range rec.pays {
		b.policies[id].remains += v
	}
	delete(b.lossSet, rec.lossID)
	b.losses = b.losses[:n-1]
}
