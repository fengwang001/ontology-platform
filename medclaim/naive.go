package medclaim

import "fmt"

// 朴素参考模型：保存每个保单全部"已生效"理赔（按受理次序），
// 每次提交或撤销后，从空账开始逐笔重放该保单所有年度的理赔，
// 以此独立验证生产引擎的增量累计账。
//
// 为保持独立性，朴素模型不读取 Engine/policyState 的任何内部状态，
// 单笔结算也不用生产 settle，而是用本文件内的 naiveSettle 重新实现。

type naiveRec struct {
	claim     *Claim
	yearIndex int
}

type naiveState struct {
	policy *Policy
	recs   []naiveRec // 受理次序
}

// naiveEngine 朴素模型引擎。
type naiveEngine struct {
	states map[string]*naiveState
}

func newNaiveEngine() *naiveEngine {
	return &naiveEngine{states: map[string]*naiveState{}}
}

func (n *naiveEngine) register(p *Policy) {
	n.states[p.PolicyID] = &naiveState{policy: clonePolicy(p)}
}

// totals 全量重放，返回各年度累计（已扣免赔、自付）。
func (n *naiveEngine) totals(policyID string) map[int][2]int {
	st := n.states[policyID]
	accs := map[int]*yearAcc{}
	for _, r := range st.recs {
		acc := accs[r.yearIndex]
		if acc == nil {
			acc = &yearAcc{yearIndex: r.yearIndex}
			accs[r.yearIndex] = acc
		}
		naiveSettle(st.policy, r.claim, acc)
	}
	out := make(map[int][2]int, len(accs))
	for yi, a := range accs {
		out[yi] = [2]int{a.deductibleUsed, a.oopTotal}
	}
	return out
}

// submit 做与生产引擎同序的校验，通过则追加并重放。
func (n *naiveEngine) submit(c *Claim) (*SettlementResult, error) {
	if err := validateClaim(c); err != nil {
		return nil, err
	}
	st, ok := n.states[c.PolicyID]
	if !ok {
		return nil, newError(ErrPolicyNotFound, "保单不存在")
	}
	for _, r := range st.recs {
		if r.claim.ClaimID == c.ClaimID {
			return nil, newError(ErrClaimExists, "理赔已存在")
		}
	}
	yi := policyYear(st.policy, c.EventDay)
	if yi < 0 {
		return nil, newError(ErrDateNotCovered, "事故日未承保")
	}
	cc := cloneClaim(c)
	st.recs = append(st.recs, naiveRec{claim: cc, yearIndex: yi})

	// 重放到最后一笔，取出其结果。
	accs := map[int]*yearAcc{}
	var last *SettlementResult
	for _, r := range st.recs {
		acc := accs[r.yearIndex]
		if acc == nil {
			acc = &yearAcc{yearIndex: r.yearIndex}
			accs[r.yearIndex] = acc
		}
		last = naiveSettle(st.policy, r.claim, acc)
	}
	return last, nil
}

func (n *naiveEngine) cancel(policyID, claimID string) error {
	st, ok := n.states[policyID]
	if !ok {
		return newError(ErrPolicyNotFound, "保单不存在")
	}
	idx := -1
	yi := -1
	for i, r := range st.recs {
		if r.claim.ClaimID == claimID {
			idx, yi = i, r.yearIndex
		}
	}
	if idx < 0 {
		return newError(ErrClaimNotFound, "理赔不存在")
	}
	// 该年度最后一笔？
	last := -1
	for i := len(st.recs) - 1; i >= 0; i-- {
		if st.recs[i].yearIndex == yi {
			last = i
			break
		}
	}
	if last != idx {
		return newError(ErrNotLast, "非末笔")
	}
	st.recs = append(st.recs[:idx], st.recs[idx+1:]...)
	return nil
}

// naiveSettle 独立重写的单笔结算：先算出每条明细的免赔消耗与比例分摊，
// 再以"年度自付封顶"为硬约束做一次截断，写法刻意与 settle 不同
// （先全量预算、后截断归并），便于交叉验证。
func naiveSettle(p *Policy, c *Claim, acc *yearAcc) *SettlementResult {
	type line struct {
		ir ItemResult
	}
	lines := make([]line, len(c.Items))
	covIdx := []int{}
	base := 0
	for i, it := range c.Items {
		lines[i].ir = ItemResult{Index: i, Code: it.Code, Category: it.Category, Amount: it.Amount}
		if p.isExcluded(it.Code) {
			lines[i].ir.Excluded = true
			continue
		}
		covIdx = append(covIdx, i)
		base += it.Amount
	}

	wantDed := 0
	if acc.oopTotal < p.AnnualOOPCap {
		wantDed = p.PerClaimDeductible
	}
	if d := p.AnnualDeductCap - acc.deductibleUsed; d < wantDed {
		wantDed = d
	}
	if wantDed > base {
		wantDed = base
	}
	if wantDed < 0 {
		wantDed = 0
	}

	// 1) 免赔按序逐条消耗。
	left := wantDed
	for _, i := range covIdx {
		amt := lines[i].ir.Amount
		d := left
		if d > amt {
			d = amt
		}
		lines[i].ir.Deductible = d
		left -= d
	}
	// 2) 剩余部分按类别比例赔付（向上取整）。
	for _, i := range covIdx {
		ir := &lines[i].ir
		rest := ir.Amount - ir.Deductible
		ins := (rest*p.ratioFor(ir.Category) + 99) / 100
		ir.InsurerPaid = ins
		ir.OOP = rest - ins
	}
	// 3) 自付封顶：逐明细累计被保人流出（免赔+比例自付），
	//    首次超 room 的明细承担截断并标记，其后明细全额转保险公司。
	room := p.AnnualOOPCap - acc.oopTotal
	if room < 0 {
		room = 0
	}
	done := false
	totalIns, totalOOP, totalDedBorne := 0, 0, 0
	for _, i := range covIdx {
		ir := &lines[i].ir
		ir.OOP += ir.Deductible // 明细自付含免赔扣除与比例自付
		if done {
			ir.InsurerPaid += ir.OOP
			ir.OOP = 0
			continue
		}
		flow := ir.OOP
		if flow <= room {
			room -= flow
			totalDedBorne += ir.Deductible
		} else {
			ir.InsurerPaid = ir.Amount - room
			borne := ir.Deductible
			if borne > room {
				borne = room
			}
			totalDedBorne += borne
			ir.OOP = room
			ir.CapTruncated = true
			room = 0
			done = true
		}
	}

	res := &SettlementResult{ClaimID: c.ClaimID, PolicyYear: acc.yearIndex,
		DeductibleUsed: wantDed, Items: make([]ItemResult, len(c.Items))}
	for i, l := range lines {
		res.Items[i] = l.ir
		totalIns += l.ir.InsurerPaid
		totalOOP += l.ir.OOP
	}
	res.InsurerPaid = totalIns
	res.OOP = totalOOP

	acc.deductibleUsed += totalDedBorne
	acc.oopTotal += totalOOP
	if acc.oopTotal > p.AnnualOOPCap {
		acc.oopTotal = p.AnnualOOPCap
	}
	return res
}

// describeClaim 以稳定文本格式打印一笔理赔输入，供日志核对。
func describeClaim(c *Claim) string {
	s := fmt.Sprintf("Submit policy=%s claim=%s day=%d items=[", c.PolicyID, c.ClaimID, c.EventDay)
	for i, it := range c.Items {
		if i > 0 {
			s += ", "
		}
		cat := "住院"
		if it.Category == CategoryOutpatient {
			cat = "门诊"
		}
		s += fmt.Sprintf("{%s %s %d分}", it.Code, cat, it.Amount)
	}
	return s + "]"
}

// describeResult 打印结算输出与关键判定依据。
func describeResult(r *SettlementResult) string {
	if r == nil {
		return "result=nil"
	}
	s := fmt.Sprintf("year=%d 免赔=%d 赔付=%d 自付=%d 明细=[",
		r.PolicyYear, r.DeductibleUsed, r.InsurerPaid, r.OOP)
	for i, ir := range r.Items {
		if i > 0 {
			s += ", "
		}
		tag := ""
		if ir.Excluded {
			tag = " 不赔"
		}
		if ir.CapTruncated {
			tag += " 封顶截断"
		}
		s += fmt.Sprintf("{%s 额%d 免%d 赔%d 付%d%s}",
			ir.Code, ir.Amount, ir.Deductible, ir.InsurerPaid, ir.OOP, tag)
	}
	return s + "]"
}
