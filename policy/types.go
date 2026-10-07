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

// Verdict 出险判定结论。
type Verdict int

const (
	PayFull        Verdict = iota // 有效：全额赔付
	PayReduced                    // 宽限中：全额减去该期欠缴保费
	DenyWaiting                   // 等待期内：不赔付（与中止不赔付可区分）
	DenyLapsed                    // 中止：不赔付
	DenyTerminated                // 终止：不赔付
)

func (v Verdict) String() string {
	switch v {
	case PayFull:
		return "全额赔付"
	case PayReduced:
		return "赔付(扣除欠缴保费)"
	case DenyWaiting:
		return "等待期内不赔付"
	case DenyLapsed:
		return "中止不赔付"
	case DenyTerminated:
		return "终止不赔付"
	}
	return "未知"
}

// 可区分的错误，拒绝次序固定为：
// 参数非法 > 保单不存在 > 时钟回退 > 状态不允许 > 补缴不足 > 超额还款。
var (
	ErrInvalidParam        = errors.New("参数非法")
	ErrPolicyNotFound      = errors.New("保单不存在")
	ErrClockRollback       = errors.New("时钟回退")
	ErrStateNotAllowed     = errors.New("状态不允许")
	ErrInsufficientPayment = errors.New("补缴不足")
	ErrOverpayment         = errors.New("超额还款")
)

// Config 保单登记参数。金额单位为分，日利率单位为万分之一。
type Config struct {
	EffectiveDay int     // 登记生效日（非负整数天）
	PeriodDays   int     // 缴费周期（正整数天）
	Premium      int64   // 每期保费（正整数分）
	GraceDays    int     // 宽限天数（正整数）
	RevivalDays  int     // 复效期天数（正整数）
	WaitingDays  int     // 等待天数（非负整数）
	CashValue    []int64 // 现金价值表：CashValue[i] 为已缴 i 期时的现金价值（分），非递减
	DailyRatePPM int64   // 借款日利率（万分之一，非负）
}

func (c Config) validate() error {
	if c.EffectiveDay < 0 || c.PeriodDays <= 0 || c.Premium <= 0 ||
		c.GraceDays <= 0 || c.RevivalDays <= 0 || c.WaitingDays < 0 ||
		c.DailyRatePPM < 0 {
		return ErrInvalidParam
	}
	if len(c.CashValue) == 0 {
		return ErrInvalidParam
	}
	for i, v := range c.CashValue {
		if v < 0 || (i > 0 && v < c.CashValue[i-1]) {
			return ErrInvalidParam
		}
	}
	return nil
}

// Snapshot 某一时刻的保单完整视图，重放相同操作序列必然得到相同快照。
type Snapshot struct {
	Day           int   // 当前时刻
	State         State // 当前状态
	PaidCount     int   // 已缴期数（含首期与垫交期）
	NextDueDay    int   // 下一期应缴日
	GraceDueDay   int   // 宽限中：该期应缴日，否则 -1
	LapseDay      int   // 中止起算日，未中止过为 -1
	LoanPrincipal int64 // 借款本金（分）
	LoanInterest  int64 // 截至当日的借款利息（分）
	OwedAmount    int64 // 欠费金额：到期未缴保费 + 借款本息（终止后为 0）
	InWaiting     bool  // 是否处于等待期
	SettleOps     int64 // 累计状态推进事件数（性能验证用）
}

// ClaimResult 出险判定结果。
type ClaimResult struct {
	Verdict   Verdict // 判定结论
	Deduction int64   // 宽限中赔付时扣除的该期欠缴保费（分），其余为 0
}
