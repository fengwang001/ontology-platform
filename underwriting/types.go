// Package underwriting 实现人身险投保单的核保决策引擎。
package underwriting

import "errors"

// 可区分错误，调用方用 errors.Is 判别。
var (
	ErrInvalidParam        = errors.New("参数非法")
	ErrApplicationNotFound = errors.New("投保单不存在")
	ErrTerminal            = errors.New("已终态")
	ErrPostponing          = errors.New("延期中")
	ErrExamRegistered      = errors.New("已登记体检")
	ErrRuleDuplicate       = errors.New("规则重复")
	ErrRuleNotFound        = errors.New("规则不存在")
	ErrApplicationExists   = errors.New("投保单已存在")
	ErrInvalidState        = errors.New("当前状态不允许该操作")
)

// ActionKind 是规则裁定动作类别，一条规则只带一种动作。
type ActionKind int

const (
	ActionStandard     ActionKind = iota // 标准承保
	ActionExtraPremium                   // 加费（百分比）
	ActionExclusion                      // 附加除外责任
	ActionPostpone                       // 延期至某时刻
	ActionDecline                        // 拒保
)

// Action 是裁定动作，仅与 Kind 对应的字段有意义。
type Action struct {
	Kind          ActionKind
	Percent       int    // ActionExtraPremium：加费百分比，正整数
	ExclusionCode string // ActionExclusion：除外编码
	PostponeUntil int64  // ActionPostpone：延期至该时刻（秒）
}

// Condition 是匹配条件，三部分同时成立才算命中；各字段缺省表示不限。
type Condition struct {
	MinAge      *int            // 年龄下界（含），nil 表示不限
	MaxAge      *int            // 年龄上界（含），nil 表示不限
	Occupations map[int]bool    // 职业类别集合，空表示不限
	Disclosures map[string]bool // 健康告知项集合（任一交集即中），空表示不限
}

// Rule 是核保规则。
type Rule struct {
	ID     string
	Cond   Condition
	Action Action
	Start  int64 // 生效区间左端（含），秒
	End    int64 // 生效区间右端（不含），秒
}

// Application 是投保单登记信息。
type Application struct {
	ID          string
	Age         int // 被保人年龄，非负整数岁
	Occupation  int // 职业类别，1 到 6
	Disclosures []string
	SumAssured  int64 // 申请保额，正整数，以分计
	AppliedAt   int64 // 申请时刻，非负整数秒
	PremiumCap  int   // 保单约定的加费上限（百分比），非负
}

// DecisionKind 是裁定结论类别。
type DecisionKind int

const (
	DecAccept      DecisionKind = iota // 承保
	DecRuleDecline                     // 规则拒保（终态）
	DecCapExceeded                     // 加费超限拒保（终态）
	DecPostpone                        // 延期
	DecNeedExam                        // 需体检
)

// Decision 是裁定结论。
type Decision struct {
	Kind          DecisionKind
	ExtraPremium  int      // DecAccept：合计加费百分比
	Exclusions    []string // DecAccept：除外责任编码并集，升序
	PostponeUntil int64    // DecPostpone：延期时刻（命中规则中最晚者）
}
