package ontology

// Clause 为分摊条款类型，仅允许以下三种取值。
type Clause int

const (
	ClauseLimitProportional    Clause = iota // 限额比例型
	ClauseIndependentLiability               // 独立责任型
	ClauseExcess                             // 超额型
)

// Policy 为保单登记信息。金额单位：分；承保区间 [CoverFrom, CoverTo)，以整数天计。
type Policy struct {
	Insured      string
	PolicyID     string
	Deductible   int64 // 每次损失免赔额，非负
	PerLossLimit int64 // 每次损失限额，正
	AnnualLimit  int64 // 年度累计限额，正
	CoverFrom    int   // 承保区间左端（含）
	CoverTo      int   // 承保区间右端（不含）
	Clause       Clause
}

// ErrorCode 为可区分的拒绝原因。拒绝次序固定为
// ErrInvalid > ErrInsuredMissing > ErrPolicyDuplicate >
// ErrLossExists > ErrLossMissing > ErrNotLast。
type ErrorCode int

const (
	ErrInvalid ErrorCode = iota + 1
	ErrInsuredMissing
	ErrPolicyDuplicate
	ErrLossExists
	ErrLossMissing
	ErrNotLast
)

type EngineError struct{ Code ErrorCode }

func (e *EngineError) Error() string {
	switch e.Code {
	case ErrInvalid:
		return "参数非法"
	case ErrInsuredMissing:
		return "被保人不存在"
	case ErrPolicyDuplicate:
		return "保单重复"
	case ErrLossExists:
		return "损失已存在"
	case ErrLossMissing:
		return "损失不存在"
	case ErrNotLast:
		return "非末笔"
	default:
		return "未知错误"
	}
}

func errCode(code ErrorCode) error { return &EngineError{Code: code} }

func validatePolicy(p Policy) error {
	if p.PolicyID == "" || p.Insured == "" ||
		p.Deductible < 0 || p.PerLossLimit <= 0 || p.AnnualLimit <= 0 ||
		p.CoverFrom >= p.CoverTo ||
		(p.Clause != ClauseLimitProportional &&
			p.Clause != ClauseIndependentLiability &&
			p.Clause != ClauseExcess) {
		return errCode(ErrInvalid)
	}
	return nil
}

func validateLoss(l Loss) error {
	if l.LossID == "" || l.Insured == "" || l.Amount <= 0 {
		return errCode(ErrInvalid)
	}
	return nil
}
