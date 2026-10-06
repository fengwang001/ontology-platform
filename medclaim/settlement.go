package medclaim

// Claim 一笔理赔申请。
type Claim struct {
	PolicyID string
	ClaimID  string // 同一保单内唯一
	EventDay int    // 事故日（相对纪元的整数天）
	Items    []Item
}

// ItemResult 一条明细的结算明细。
type ItemResult struct {
	Index        int
	Code         string
	Category     Category
	Amount       int
	Excluded     bool // 命中不赔集合
	Deductible   int  // 本明细被免赔消耗的金额
	InsurerPaid  int  // 保险公司赔付
	OOP          int  // 被保人自付（含免赔与比例自付）
	CapTruncated bool // 是否为封顶截断的最后一条明细
}

// SettlementResult 单笔理赔结算结果。
type SettlementResult struct {
	ClaimID        string
	PolicyYear     int
	InsurerPaid    int // 保险公司赔付合计
	OOP            int // 被保人自付合计（不含不赔项目）
	DeductibleUsed int // 本次免赔扣除额
	Items          []ItemResult
}

// yearAcc 保单年度累计账（结算次序推进）。
type yearAcc struct {
	yearIndex      int
	deductibleUsed int // 本年度已扣免赔累计
	oopTotal       int // 本年度自付累计
}

// settle 在给定年度累计账上执行单笔结算，并就地推进 acc。
//
// 语义（金额单位：分）：
//  1. 命中不赔集合的明细整条剔除，不进入免赔与自付累计；
//  2. 本次事故免赔额为 min(每次事故免赔, 年度免赔上限-已扣免赔, 可赔基数)，
//     且当自付累计已达年度自付封顶时本次免赔为 0；
//  3. 免赔按明细次序逐条消耗（先列先扣尽）；
//  4. 每条明细剩余部分按类别比例赔付，赔付额向上取整（ceil），余归自付；
//  5. 自付（免赔扣除与比例自付合计）不得使年度自付累计超过封顶：
//     超出部分由保险公司承担；截断仅发生一次（CapTruncated 标记该条），
//     其后明细剩余金额全额赔付、免赔继续消耗时也由保险公司承担。
func settle(p *Policy, claim *Claim, acc *yearAcc) *SettlementResult {
	res := &SettlementResult{
		ClaimID:    claim.ClaimID,
		PolicyYear: acc.yearIndex,
		Items:      make([]ItemResult, 0, len(claim.Items)),
	}

	type covered struct {
		idx int
		it  Item
	}
	cov := make([]covered, 0, len(claim.Items))
	for idx, it := range claim.Items {
		if p.isExcluded(it.Code) {
			res.Items = append(res.Items, ItemResult{
				Index: idx, Code: it.Code, Category: it.Category,
				Amount: it.Amount, Excluded: true,
			})
			continue
		}
		cov = append(cov, covered{idx, it})
	}

	base := 0
	for _, c := range cov {
		base += c.it.Amount
	}

	remainingDed := 0
	if acc.oopTotal < p.AnnualOOPCap {
		remainingDed = p.PerClaimDeductible
		if r := p.AnnualDeductCap - acc.deductibleUsed; r < remainingDed {
			remainingDed = r
		}
		if remainingDed > base {
			remainingDed = base
		}
		if remainingDed < 0 {
			remainingDed = 0
		}
	}
	res.DeductibleUsed = remainingDed

	room := p.AnnualOOPCap - acc.oopTotal
	if room < 0 {
		room = 0
	}
	truncated := false
	dedBorneTotal := 0
	out := make([]ItemResult, len(claim.Items))
	for _, ir := range res.Items {
		if ir.Excluded {
			out[ir.Index] = ir
		}
	}

	for _, c := range cov {
		ir := ItemResult{Index: c.idx, Code: c.it.Code, Category: c.it.Category, Amount: c.it.Amount}
		amt := c.it.Amount

		ded := remainingDed
		if ded > amt {
			ded = amt
		}
		remainingDed -= ded
		rest := amt - ded

		ratio := p.ratioFor(c.it.Category)
		ins := ceilDiv(rest*ratio, 100)
		oop := ded + rest - ins // 自付含免赔扣除与比例自付

		if !truncated {
			switch {
			case oop <= room:
				room -= oop
				dedBorneTotal += ded
			default:
				// 触及封顶（无论在免赔阶段还是比例自付阶段）：本条被保人
				// 自付恰为剩余 room，其余金额全部由保险公司承担，本条即
				// 最后一条被截断明细，其后所有金额全额赔付。
				// 免赔消耗额不改小（免赔已按序扣尽）；被保人实担的免赔
				// 最多为 room，超出部分由保险公司承担、不计入年度免赔累计。
				borne := ded
				if borne > room {
					borne = room
				}
				dedBorneTotal += borne
				ins = amt - room
				oop = room
				room = 0
				truncated = true
				ir.CapTruncated = true
			}
		} else {
			// 封顶已在之前的明细触发：免赔照常消耗但本明细金额全部由
			// 保险公司承担，被保人自付为 0。
			ins = amt
			oop = 0
		}

		ir.Deductible = ded
		ir.InsurerPaid = ins
		ir.OOP = oop
		out[c.idx] = ir

		res.InsurerPaid += ins
		res.OOP += oop
	}

	res.Items = out

	// 年度免赔累计只记被保人实际承担的免赔；封顶后由保险公司承担的
	// 免赔消耗不形成被保人的免赔累计。
	acc.deductibleUsed += dedBorneTotal
	acc.oopTotal += res.OOP
	if acc.oopTotal > p.AnnualOOPCap {
		acc.oopTotal = p.AnnualOOPCap
	}
	return res
}

// ceilDiv 返回 ceil(a/b)，要求 a >= 0, b > 0。
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
