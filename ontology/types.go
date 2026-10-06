package ontology

// Category 标识费用明细的赔付类别。
type Category int

const (
	// CatUnknown 为非法类别的零值占位。
	CatUnknown Category = iota
	CatInpatient
	CatOutpatient
)

// Line 是一笔理赔中的一条费用明细。
type Line struct {
	Code     string
	Category Category
	Amount   int64 // 正整数分
}

// Claim 是一次理赔的输入。
type Claim struct {
	ID     string
	AccDay int64 // 事故日（自承保起始日起的非负整数天基准下的绝对天序号）
	Lines  []Line
}

// PolicySpec 是保单登记参数。所有金额均为非负整数分。
type PolicySpec struct {
	InceptDay          int64           // 承保起始日（非负整数天）
	YearLen            int64           // 年度长度（正整数天），年度区间左闭右开
	PerClaimDeductible int64           // 每次事故免赔额
	AnnualDeductCap    int64           // 年度免赔累计上限
	InpatientRate      int             // 住院赔付比例（0..100）
	OutpatientRate     int             // 门诊赔付比例（0..100）
	OOPCap             int64           // 年度自付封顶额
	ExcludedCodes      map[string]bool // 不赔项目编码集合
}

// LineOut 是单条可赔明细的归属结果（不赔明细不出现在此列表）。
type LineOut struct {
	Code      string
	Category  Category
	Amount    int64 // 明细原金额
	Deduct    int64 // 本明细实际消耗的事故免赔
	Insurer   int64 // 保险公司赔付
	SelfPay   int64 // 被保人自付
	CapShift  int64 // 因自付封顶而改由保险公司承担的追加赔付（仅归属到最后一条被截断明细）
	FullAfter bool  // 封顶后全额赔付的明细
}

// Settlement 是一笔理赔的结算结果。
type Settlement struct {
	ClaimID        string
	YearIndex      int64
	CoveredBase    int64 // 可赔基数（排除不赔项目后的合计）
	DeductApplied  int64 // 本次实际扣除免赔
	InsurerPay     int64 // 保险公司赔付总额
	SelfPay        int64 // 被保人自付总额
	ExcludedAmount int64 // 不赔项目金额
	Lines          []LineOut
}
