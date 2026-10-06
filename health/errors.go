package health

import "errors"

// 可区分的业务错误，使用 errors.Is 比较。
var (
	ErrInvalidArgument = errors.New("参数非法")
	ErrInsuredNotFound = errors.New("被保人不存在")
	ErrCodeNotFound    = errors.New("编码不存在")
	ErrCodeDuplicate   = errors.New("编码重复")
	ErrParentNotFound  = errors.New("上级不存在")
	ErrIntervalOverlap = errors.New("区间重叠")
	ErrClaimExists     = errors.New("理赔已存在")
	ErrUninsuredDate   = errors.New("出险日未承保")
)

// CodeInput 登记或变更一个疾病编码。Parent 为空表示顶级编码。
type CodeInput struct {
	Code     string
	Parent   string
	Accident bool
}

// PolicyInput 保单登记参数。所有日期均为非负整数天，金额为正整数分。
type PolicyInput struct {
	Person       string
	RegisteredAt int64
	Start        int64
	End          int64
	WaitDays     int64
	Amount       int64
	Declared     []string
}

// Diagnosis 理赔中的单个诊断。
type Diagnosis struct {
	Code string
	Fee  int64
}

// ClaimInput 一笔理赔。
type ClaimInput struct {
	ID        string
	Person    string
	Day       int64
	Diagnoses []Diagnosis
}

// 单个诊断的判定依据，三种取值互斥、可区分。
const (
	ReasonExcluded = "excluded" // 既往症除外
	ReasonWaiting  = "waiting"  // 等待期内不赔
	ReasonPaid     = "paid"     // 可赔付
)

// DiagnosisVerdict 单个诊断的判定结果。
type DiagnosisVerdict struct {
	Code   string
	Fee    int64
	Reason string
}

// ClaimResult 理赔结果：Payout 为合计赔付（分），Verdicts 与输入诊断同序。
type ClaimResult struct {
	Payout   int64
	Verdicts []DiagnosisVerdict
}
