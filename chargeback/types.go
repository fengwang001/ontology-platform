// Package chargeback 实现银行卡拒付案件管理与商户资金扣回系统。
//
// 覆盖提起、应诉、发卡行审阅、预仲裁与终局裁决的完整生命周期。
// 时间为整数天，所有操作携带 now；逾期自动认定通过惰性推导完成，
// 任意时刻的查询结果只取决于已接受操作序列与查询所用的 now。
package chargeback

// Reason 是拒付原因。
type Reason int

const (
	ReasonFraud       Reason = iota // 欺诈
	ReasonNotReceived               // 未收货
	ReasonDuplicate                 // 重复扣款
)

// Valid 报告原因是否为合法枚举值。
func (r Reason) Valid() bool {
	return r == ReasonFraud || r == ReasonNotReceived || r == ReasonDuplicate
}

// State 是案件在某一时刻的（推导）状态。
type State int

const (
	StateOpened            State = iota // 已提起，等待商户应诉
	StateAwaitingReview                 // 已应诉，等待发卡行审阅
	StatePreArbitration                 // 预仲裁中，等待裁决
	StateClosedMerchantWin              // 终局：商户胜
	StateClosedIssuerWin                // 终局：发卡行胜
)

func (s State) String() string {
	switch s {
	case StateOpened:
		return "Opened"
	case StateAwaitingReview:
		return "AwaitingReview"
	case StatePreArbitration:
		return "PreArbitration"
	case StateClosedMerchantWin:
		return "ClosedMerchantWin"
	case StateClosedIssuerWin:
		return "ClosedIssuerWin"
	}
	return "Unknown"
}

// Closed 报告状态是否为终局。
func (s State) Closed() bool {
	return s == StateClosedMerchantWin || s == StateClosedIssuerWin
}

// Outcome 是裁决结果。
type Outcome int

const (
	OutcomeNone        Outcome = iota // 尚未裁决
	OutcomeMerchantWin                // 商户胜
	OutcomeIssuerWin                  // 发卡行胜
)

// Valid 报告裁决结果是否为合法枚举值（OutcomeNone 不合法）。
func (o Outcome) Valid() bool {
	return o == OutcomeMerchantWin || o == OutcomeIssuerWin
}

// Config 是系统全部可配置参数。天数均为整数天，金额为最小货币单位。
type Config struct {
	FraudWindowDays       int   // 欺诈原因提起窗口
	NotReceivedWindowDays int   // 未收货原因提起窗口
	DuplicateWindowDays   int   // 重复扣款原因提起窗口
	DuplicateMatchDays    int   // 重复扣款依据交易的结算日最大间隔
	ResponseWindowDays    int   // R：商户应诉期（提起日起，含第 R 天）
	ReviewWindowDays      int   // P：发卡行审阅期（应诉日起，含第 P 天）
	ArbitrationFee        int64 // 败诉方承担的固定仲裁费
}

// WindowDays 返回指定原因的提起窗口天数。
func (c Config) WindowDays(r Reason) int {
	switch r {
	case ReasonFraud:
		return c.FraudWindowDays
	case ReasonNotReceived:
		return c.NotReceivedWindowDays
	case ReasonDuplicate:
		return c.DuplicateWindowDays
	}
	return 0
}

// Transaction 是一笔已结算交易。金额为正整数最小货币单位。
type Transaction struct {
	ID         string
	SettleDay  int
	Amount     int64
	CardID     string
	MerchantID string
}
