package apportion

// 可区分的规则错误。拒绝次序：参数非法 > 被保人不存在 > 保单重复 >
// 损失已存在 > 损失不存在 > 非末笔。
const (
	ErrInvalid        = "参数非法"
	ErrInsuredMissing = "被保人不存在"
	ErrPolicyDup      = "保单重复"
	ErrLossDup        = "损失已存在"
	ErrLossMissing    = "损失不存在"
	ErrNotLast        = "非末笔"
)

// Error 是带错误码的规则错误，错误码即对外的拒绝结论。
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

func ruleError(code string) *Error { return &Error{Code: code} }

// Clause 为分摊条款类型，仅三种。
type Clause int

const (
	// LimitShare 限额比例型：按每次损失限额与年度累计剩余较小者占比分摊。
	LimitShare Clause = iota + 1
	// IndependentShare 独立责任型：出现任一张即令非超额组整体改按独立责任额分摊。
	IndependentShare
	// Excess 超额型：仅在非超额组未赔足时，对剩余部分按独立责任额分摊。
	Excess
)

func (c Clause) valid() bool { return c >= LimitShare && c <= Excess }

// Policy 为保单登记信息。金额单位为分，承保区间 [StartDay, EndDay)，以整数天计。
type Policy struct {
	Insured     string
	PolicyNo    string
	Deductible  int64 // 每次损失免赔额，非负
	PerLoss     int64 // 每次损失限额，正
	AnnualLimit int64 // 年度累计限额，正
	StartDay    int64 // 承保区间左端（含）
	EndDay      int64 // 承保区间右端（不含），须大于 StartDay
	Clause      Clause
}

func (p Policy) validate() error {
	if p.Insured == "" || p.PolicyNo == "" ||
		p.Deductible < 0 || p.PerLoss <= 0 || p.AnnualLimit <= 0 ||
		p.StartDay >= p.EndDay || !p.Clause.valid() {
		return ruleError(ErrInvalid)
	}
	return nil
}

// covers 判断保单是否覆盖损失日（左含右不含）。
func (p Policy) covers(day int64) bool { return day >= p.StartDay && day < p.EndDay }

// Loss 为一次损失受理请求。金额单位为分。
type Loss struct {
	LossNo  string
	Insured string
	Day     int64
	Amount  int64 // 正整数分
}

func (l Loss) validate() error {
	if l.LossNo == "" || l.Insured == "" || l.Amount <= 0 {
		return ruleError(ErrInvalid)
	}
	return nil
}

// participant 为某次分摊中的保单快照。
type participant struct {
	policy    *Policy
	remaining int64 // 年度累计剩余（受理时刻）
	il        int64 // 独立责任额
}

// independentLiability 计算独立责任额：假设仅本保单承保时，按免赔与限额应赔，
// 且不超过年度累计剩余。
func independentLiability(p *Policy, remaining, amount int64) int64 {
	pay := amount - p.Deductible
	if pay < 0 {
		pay = 0
	}
	if pay > p.PerLoss {
		pay = p.PerLoss
	}
	if pay > remaining {
		pay = remaining
	}
	return pay
}
