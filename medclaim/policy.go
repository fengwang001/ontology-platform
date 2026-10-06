package medclaim

// Category 费用类别。
type Category int

const (
	CategoryInpatient  Category = iota + 1 // 住院
	CategoryOutpatient                     // 门诊
)

// Item 一条费用明细。
type Item struct {
	Code     string   // 项目编码
	Category Category // 类别
	Amount   int      // 金额（分，正整数）
}

// Policy 保单条款登记。金额单位均为分（非负整数）。
type Policy struct {
	PolicyID           string
	InceptionDay       int                 // 承保起始日（相对纪元的非负整数天）
	YearLength         int                 // 年度长度（正整数天），年度区间 [起, 起+长)
	PerClaimDeductible int                 // 每次事故免赔额
	AnnualDeductCap    int                 // 年度免赔累计上限
	InpatientRatio     int                 // 住院赔付比例（0..100）
	OutpatientRatio    int                 // 门诊赔付比例（0..100）
	AnnualOOPCap       int                 // 年度自付封顶额
	ExcludedCodes      map[string]struct{} // 不赔项目编码集合
}

// validatePolicy 校验保单条款参数。
// 承保起始日须非负；年度长度须为正；免赔、封顶金额须非负；
// 赔付比例须为 0..100 的整数。
func validatePolicy(p *Policy) error {
	if p == nil {
		return newError(ErrInvalidParameter, "保单为空")
	}
	if p.PolicyID == "" {
		return newError(ErrInvalidParameter, "保单号为空")
	}
	if p.InceptionDay < 0 {
		return newError(ErrInvalidParameter, "承保起始日为负")
	}
	if p.YearLength <= 0 {
		return newError(ErrInvalidParameter, "年度长度非正")
	}
	if p.PerClaimDeductible < 0 || p.AnnualDeductCap < 0 || p.AnnualOOPCap < 0 {
		return newError(ErrInvalidParameter, "金额为负")
	}
	if p.InpatientRatio < 0 || p.InpatientRatio > 100 ||
		p.OutpatientRatio < 0 || p.OutpatientRatio > 100 {
		return newError(ErrInvalidParameter, "赔付比例越界")
	}
	return nil
}

// validateClaim 校验理赔参数：明细须非空，金额须为正，类别须已知。
func validateClaim(c *Claim) error {
	if c == nil {
		return newError(ErrInvalidParameter, "理赔为空")
	}
	if c.ClaimID == "" {
		return newError(ErrInvalidParameter, "理赔号为空")
	}
	if len(c.Items) == 0 {
		return newError(ErrInvalidParameter, "明细为空")
	}
	for _, it := range c.Items {
		if it.Amount <= 0 {
			return newError(ErrInvalidParameter, "明细金额非正")
		}
		if it.Category != CategoryInpatient && it.Category != CategoryOutpatient {
			return newError(ErrInvalidParameter, "类别未知")
		}
	}
	return nil
}

// policyYear 返回事故日所属保单年度序号（自 0 起）。
// 年度区间为 [InceptionDay + k*YearLength, InceptionDay + (k+1)*YearLength)。
// day 早于承保起始日时返回 -1。
func policyYear(p *Policy, day int) int {
	if day < p.InceptionDay {
		return -1
	}
	return (day - p.InceptionDay) / p.YearLength
}

// ratioFor 返回某类别的赔付比例。
func (p *Policy) ratioFor(cat Category) int {
	if cat == CategoryInpatient {
		return p.InpatientRatio
	}
	return p.OutpatientRatio
}

// isExcluded 判断项目编码是否命中不赔集合。
func (p *Policy) isExcluded(code string) bool {
	_, ok := p.ExcludedCodes[code]
	return ok
}
