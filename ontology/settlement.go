package ontology

// 结算语义（金额均为整数分）：
//  1. 命中不赔集合的明细整条剔除，金额既不计免赔也不计自付。
//  2. 可赔基数 = 其余明细金额合计。
//  3. 本次免赔额 = min(每次事故免赔, 年度免赔上限-已扣免赔, 可赔基数)，
//     按明细次序逐条消耗，先列先扣尽。
//  4. 每条明细剩余部分按类别比例赔付，向上取整到分，其余为比例自付。
//  5. 自付封顶：若本笔自付将使年度累计超过封顶，超出部分改由保险公司承担，
//     年度累计恰好等于封顶；追加赔付归属到“最后一条被截断的明细”，
//     其后的明细全额赔付（Insurer=原金额）。自付累计已达封顶时，
//     本笔可赔明细全部全额赔付、不再扣免赔。

// settle 是无状态的单笔结算纯函数。
//
// in 为本笔理赔；spec 为保单条款；yearDeduct 为本年度已扣免赔；
// yearOOP 为本年度自付累计。返回结算结果以及结算后的年度累计
// （新已扣免赔、新自付累计）。该函数不做任何参数校验，校验由引擎负责。
type workLine struct {
	l        Line
	amount   int64 // 原金额
	deduct   int64 // 消耗免赔（名义，步骤3分配）
	insurer  int64
	self     int64
	full     bool  // 封顶后全额赔付
	capShift int64 // 封顶追加赔付
	rate     int   // 类别比例（0..100）
}

func settle(in Claim, spec PolicySpec, yearDeduct, yearOOP int64) (Settlement, int64, int64) {
	out := Settlement{ClaimID: in.ID}

	// 步骤 1：剔除不赔项目，保留可赔明细的原顺序与金额。
	works := make([]workLine, 0, len(in.Lines))
	var base int64
	for _, l := range in.Lines {
		if spec.ExcludedCodes[l.Code] {
			out.ExcludedAmount += l.Amount
			continue
		}
		rate := spec.InpatientRate
		if l.Category == CatOutpatient {
			rate = spec.OutpatientRate
		}
		works = append(works, workLine{l: l, amount: l.Amount, rate: rate})
		base += l.Amount
	}
	out.CoveredBase = base

	// 步骤 5 捷径：自付累计已恰达/超过封顶，后续可赔明细全额赔付、不扣免赔。
	if yearOOP >= spec.OOPCap {
		var insTotal int64
		for i := range works {
			works[i].deduct = 0
			works[i].insurer = works[i].amount
			works[i].self = 0
			works[i].full = true
			insTotal += works[i].amount
		}
		out.DeductApplied = 0
		out.InsurerPay = insTotal
		out.SelfPay = 0
		out.Lines = finalizeLines(works)
		return out, yearDeduct, yearOOP
	}

	// 步骤 3：计算本次免赔额并按明细次序逐条消耗。
	deduct := spec.PerClaimDeductible
	if remain := spec.AnnualDeductCap - yearDeduct; remain < deduct {
		deduct = remain
	}
	if deduct < 0 {
		deduct = 0
	}
	if deduct > base {
		deduct = base
	}
	remaining := deduct
	for i := range works {
		want := works[i].amount
		if remaining < want {
			want = remaining
		}
		works[i].deduct = want
		remaining -= want
		if remaining == 0 {
			break
		}
	}

	// 步骤 4：剩余部分按类别比例赔付（向上取整），其余为比例自付。
	var preSelf int64 // 封顶前本笔自付（免赔 + 比例自付）
	for i := range works {
		rest := works[i].amount - works[i].deduct
		paid := ceilDiv(rest*int64(works[i].rate), 100)
		works[i].insurer = paid
		works[i].self = works[i].amount - paid // 免赔部分 + 比例自付
		preSelf += works[i].self
	}

	// 步骤 5：判断是否触及自付封顶并截断。
	room := spec.OOPCap - yearOOP // 本笔最多可新增的自付（>0）
	var newDeduct, newOOP int64
	if preSelf <= room {
		// 未触顶：按原结果入账。
		var selfTotal, insTotal int64
		for i := range works {
			selfTotal += works[i].self
			insTotal += works[i].insurer
		}
		newDeduct = yearDeduct + deduct
		newOOP = yearOOP + selfTotal
		out.DeductApplied = deduct
		out.InsurerPay = insTotal
		out.SelfPay = selfTotal
		out.Lines = finalizeLines(works)
		return out, newDeduct, newOOP
	}

	// 触顶：按次序逐条放入自付，找到最后一条被截断（跨越边界）的明细。
	selfBudget := room
	var insTotal, selfTotal int64
	// cross 为“累计自付首次严格超过 room”所在明细；此前明细原样保留。
	cross := len(works) // 越界哨兵：不存在截断明细
	prefix := int64(0)
	for i := range works {
		if prefix+works[i].self > selfBudget {
			cross = i
			break
		}
		prefix += works[i].self
	}
	firstFull := cross + 1
	if cross < len(works) && selfBudget-prefix == 0 {
		// 自付恰好在此前一条达到封顶：cross 这条本身也全额赔付，无截断明细。
		firstFull = cross
	}

	// cross 之前明细保持原 insurer/self 结果。
	for i := 0; i < cross; i++ {
		insTotal += works[i].insurer
		selfTotal += works[i].self
	}

	// 截断明细：仅 (selfBudget-prefix) 归自付，其余转保险公司。
	if cross < len(works) && firstFull == cross+1 {
		allowedSelf := selfBudget - prefix
		w := &works[cross]
		w.capShift = w.self - allowedSelf
		w.insurer += w.capShift
		w.self = allowedSelf
		insTotal += w.insurer
		selfTotal += w.self
	}

	// 截断明细之后的所有可赔明细：全额赔付。
	for i := firstFull; i < len(works); i++ {
		works[i].insurer = works[i].amount
		works[i].self = 0
		works[i].deduct = 0
		works[i].full = true
		insTotal += works[i].amount
	}

	// 年度免赔账按名义免赔扣除额累计（规格固定次序：免赔先于封顶确定）。
	newDeduct = yearDeduct + deduct
	newOOP = spec.OOPCap // 恰好等于封顶
	out.DeductApplied = deduct
	out.InsurerPay = insTotal
	out.SelfPay = selfTotal
	out.Lines = finalizeLines(works)
	return out, newDeduct, newOOP
}

func finalizeLines(ws []workLine) []LineOut {
	res := make([]LineOut, 0, len(ws))
	for _, w := range ws {
		res = append(res, LineOut{
			Code:      w.l.Code,
			Category:  w.l.Category,
			Amount:    w.amount,
			Deduct:    w.deduct,
			Insurer:   w.insurer,
			SelfPay:   w.self,
			CapShift:  w.capShift,
			FullAfter: w.full,
		})
	}
	return res
}

// ceilDiv 返回 a/b 的向上取整，b 必须为正。
func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
