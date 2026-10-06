// Package apportion 实现同一损失下多张保单重复投保的分摊赔付引擎。
package apportion

import "errors"

// ClauseType 分摊条款类型。
type ClauseType int

const (
	// ClauseLimitProportional 限额比例型。
	ClauseLimitProportional ClauseType = iota
	// ClauseIndependentLiability 独立责任型。
	ClauseIndependentLiability
	// ClauseExcess 超额型。
	ClauseExcess
)

// Valid 报告条款类型是否已知。
func (c ClauseType) Valid() bool {
	return c == ClauseLimitProportional || c == ClauseIndependentLiability || c == ClauseExcess
}

func (c ClauseType) String() string {
	switch c {
	case ClauseLimitProportional:
		return "限额比例型"
	case ClauseIndependentLiability:
		return "独立责任型"
	case ClauseExcess:
		return "超额型"
	}
	return "未知条款"
}

// 可区分的各类拒绝错误，按判定优先级排列：
// 参数非法 > 被保人不存在 > 保单重复 > 损失已存在 > 损失不存在 > 非末笔。
var (
	ErrInvalidParam    = errors.New("参数非法")
	ErrInsuredNotFound = errors.New("被保人不存在")
	ErrDuplicatePolicy = errors.New("保单重复")
	ErrLossExists      = errors.New("损失已存在")
	ErrLossNotFound    = errors.New("损失不存在")
	ErrNotLastLoss     = errors.New("非末笔")
)

// Policy 保单登记信息。金额单位：分。
type Policy struct {
	InsuredID         string     // 被保人标识
	PolicyNo          string     // 保单编号（同一被保人名下唯一）
	DeductiblePerLoss int64      // 每次损失免赔额，非负
	LimitPerLoss      int64      // 每次损失限额，正整数
	AnnualLimit       int64      // 年度累计限额，正整数
	StartDay          int        // 承保区间左端（包含），整数天
	EndDay            int        // 承保区间右端（不包含），整数天
	Clause            ClauseType // 分摊条款类型
}

// Loss 一次损失。
type Loss struct {
	LossID    string // 唯一损失号
	InsuredID string // 被保人标识
	LossDay   int    // 损失日，整数天
	Amount    int64  // 损失金额（分），正整数
}

// Payout 一张保单的应赔额。
type Payout struct {
	PolicyNo string
	Amount   int64
}

// Verdict 受理结论。
type Verdict string

const (
	VerdictApportioned     Verdict = "已分摊"
	VerdictNoPayablePolicy Verdict = "无可赔保单"
)

// Result 损失受理结果。Payouts 按保单编号字典序排列，仅含应赔额为正的保单。
type Result struct {
	LossID  string
	Verdict Verdict
	Payouts []Payout
}

// Total 应赔总额。
func (r Result) Total() int64 {
	var sum int64
	for _, p := range r.Payouts {
		sum += p.Amount
	}
	return sum
}
