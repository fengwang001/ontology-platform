// Package policy 实现长期寿险保单的保费宽限、自动垫交、中止与复效状态引擎。
//
// 模块划分：schedule（缴费计划）、loan（垫交借款账）、policy（状态推进）、
// claim（出险判定）、engine（登记、并发与固定拒绝次序）。
package policy

import "errors"

// State 保单状态，只有四种。
type State int

const (
	StateActive     State = iota // 有效
	StateGrace                   // 宽限中
	StateLapsed                  // 中止
	StateTerminated              // 终止（终态）
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "有效"
	case StateGrace:
		return "宽限中"
	case StateLapsed:
		return "中止"
	case StateTerminated:
		return "终止"
	}
	return "未知"
}

// 各类错误可区分；拒绝次序固定为：
// 参数非法 > 保单不存在 > 时钟回退 > 状态不允许 > 补缴不足 > 超额还款。
var (
	ErrInvalidParam        = errors.New("参数非法")
	ErrPolicyNotFound      = errors.New("保单不存在")
	ErrClockBackward       = errors.New("时钟回退")
	ErrStateNotAllowed     = errors.New("状态不允许")
	ErrInsufficientPayment = errors.New("补缴不足")
	ErrOverpayment         = errors.New("超额还款")
)

// Config 保单登记参数。
type Config struct {
	EffectiveDay      int64   // 生效日（非负整数天）
	PeriodDays        int64   // 缴费周期（正整数天）
	PremiumCents      int64   // 每期保费（正整数分）
	GraceDays         int64   // 宽限天数（正整数，含应缴日当天）
	ReinstateDays     int64   // 复效期天数（正整数）
	WaitingDays       int64   // 等待天数（非负整数，复效后重新适用）
	SumAssuredCents   int64   // 保额（正整数分），即“赔付全额”
	LoanRatePerMyriad int64   // 借款日利率（万分之一为单位，非负）
	CashValues        []int64 // 现金价值表：CashValues[k] 为已缴 k 期的现金价值，非递减；超出表长取末项
}

func (c Config) validate() error {
	if c.EffectiveDay < 0 || c.PeriodDays <= 0 || c.PremiumCents <= 0 ||
		c.GraceDays <= 0 || c.ReinstateDays <= 0 || c.WaitingDays < 0 ||
		c.SumAssuredCents <= 0 || c.LoanRatePerMyriad < 0 {
		return ErrInvalidParam
	}
	if len(c.CashValues) == 0 {
		return ErrInvalidParam
	}
	for i, v := range c.CashValues {
		if v < 0 || (i > 0 && v < c.CashValues[i-1]) {
			return ErrInvalidParam
		}
	}
	return nil
}

// ClaimReason 出险判定结论，各类不赔付原因可区分。
type ClaimReason int

const (
	ReasonFull          ClaimReason = iota // 有效，全额赔付
	ReasonGraceDeducted                    // 宽限中，赔付全额减去欠缴保费
	ReasonWaiting                          // 等待期内，不赔付
	ReasonLapsed                           // 中止，不赔付
	ReasonTerminated                       // 终止，不赔付
)

func (r ClaimReason) String() string {
	switch r {
	case ReasonFull:
		return "有效全额赔付"
	case ReasonGraceDeducted:
		return "宽限中赔付(扣欠费)"
	case ReasonWaiting:
		return "等待期内不赔付"
	case ReasonLapsed:
		return "中止不赔付"
	case ReasonTerminated:
		return "终止不赔付"
	}
	return "未知"
}

// ClaimResult 出险判定结果。
type ClaimResult struct {
	Pay         bool
	AmountCents int64
	Reason      ClaimReason
}

// Snapshot 某一时刻的保单快照。
type Snapshot struct {
	Day           int64
	State         State
	PaidPeriods   int64
	NextDueDay    int64
	OwedCents     int64
	LoanPrincipal int64
	LoanInterest  int64
	LoanCount     int64
	WaitingUntil  int64
}
