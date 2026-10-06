package policy

// Kind 为批改类型。
type Kind int

const (
	KindAmountChange      Kind = iota + 1 // 保额变更
	KindPayPeriodChange                   // 缴费期变更
	KindBeneficiaryChange                 // 受益人变更
)

// Status 为批改状态。
type Status int

const (
	StatusScheduled   Status = iota + 1 // 预约
	StatusAwaitingPay                   // 待补缴
	StatusEffective                     // 已生效
	StatusCancelled                     // 已撤销
)

// Config 是保单登记参数，金额与保费均为非负/正整数分。
type Config struct {
	EffectiveDay   int64   // 生效日（非负整数天）
	AnnualPremium  int64   // 年缴保费（正整数分）
	BaseSumAssured int64   // 基本保额（正整数分）
	MinSumAssured  int64   // 允许的最低保额（部分退保后不得低于）
	CoolingDays    int64   // 犹豫期天数（含生效日当天）
	PolicyFee      int64   // 工本费（非负整数分）
	PayPeriods     int64   // 登记缴费期（年，正整数）
	RatioTable     []int64 // 按保单年度序号（从 1 起）给出百分比，未给出年度沿用末档
}

// EndRequest 是批改预约入参。
type EndRequest struct {
	PolicyID       string
	EndID          string
	Kind           Kind
	ApplyDay       int64
	EffectiveDay   int64
	NewSumAssured  int64  // 仅保额变更使用
	NewPayPeriods  int64  // 仅缴费期变更使用
	NewBeneficiary string // 仅受益人变更使用
}

// EndInfo 是批改的只读视图。
type EndInfo struct {
	EndID          string
	PolicyID       string
	Kind           Kind
	ApplyDay       int64
	EffectiveDay   int64
	Seq            int64
	Status         Status
	Surcharge      int64 // 生效时应补缴金额（>0）
	Refund         int64 // 生效时退还金额（>0）
	NewSumAssured  int64
	NewPayPeriods  int64
	NewBeneficiary string
}

// SurrenderResult 是退保结果。
type SurrenderResult struct {
	Payout        int64 // 实际退还金额（分）
	CashValue     int64 // 退保日现金价值（部分退保时为退保前现金价值）
	AnnualPremium int64 // 退保后年缴保费（整单退保后无意义）
	SumAssured    int64 // 退保后保额（整单退保后无意义）
}

// endorsement 是批改的内部记录。
type endorsement struct {
	kind           Kind
	policyID       string
	endID          string
	applyDay       int64
	effectiveDay   int64
	seq            int64 // 全局申请次序，同日按此次序生效
	status         Status
	newSumAssured  int64
	newPayPeriods  int64
	newBeneficiary string
	surcharge      int64
	refund         int64
}

func (e *endorsement) info() EndInfo {
	return EndInfo{
		EndID:          e.endID,
		PolicyID:       e.policyID,
		Kind:           e.kind,
		ApplyDay:       e.applyDay,
		EffectiveDay:   e.effectiveDay,
		Seq:            e.seq,
		Status:         e.status,
		Surcharge:      e.surcharge,
		Refund:         e.refund,
		NewSumAssured:  e.newSumAssured,
		NewPayPeriods:  e.newPayPeriods,
		NewBeneficiary: e.newBeneficiary,
	}
}

// policy 是保单账。
// 金额字段全部为累计量或当期量，现金价值计算不遍历历史：
// paidTotal 即“截至当前已实缴保费累计”，paidYears 用位图记录已缴年度。
type policy struct {
	id                  string
	cfg                 Config
	sumAssured          int64
	annualPremium       int64
	payPeriods          int64
	beneficiary         string
	paidTotal           int64
	paidYears           map[int64]struct{}
	loan                int64 // 未偿借款本息（外部提供）
	terminated          bool
	lastEffectiveEndDay int64 // 已生效批改中最晚的生效日
	endByID             map[string]*endorsement
	pending             map[string]*endorsement // scheduled 与 awaitingPay
	heap                endHeap                 // (effectiveDay, seq) 最小堆，只含 pending
	index               map[string]int          // endID -> heap 下标
}

// policyYear 返回 day 所在保单年度序号（自生效日起每 365 天，左闭右开）。
func policyYear(day, effectiveDay int64) int64 {
	if day < effectiveDay {
		return 1 // 生效日前保单尚未起算，当前年度记为第 1 年度
	}
	return (day-effectiveDay)/365 + 1
}

// ratioOf 返回第 year 保单年度的百分比，未给出的年度沿用末档。
func ratioOf(table []int64, year int64) int64 {
	if len(table) == 0 || year <= 0 {
		return 0
	}
	idx := int(year - 1)
	if idx >= len(table) {
		idx = len(table) - 1
	}
	return table[idx]
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }
