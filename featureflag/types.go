// Package featureflag 实现特性开关规则集的发布与求值。
package featureflag

// RuleSet 是一次发布的完整规则集：开关名 -> 开关定义。
type RuleSet map[string]*SwitchDef

// SwitchDef 描述单个特性开关。
type SwitchDef struct {
	// Enabled 为 false 时求值直接返回 OffVariant。
	Enabled bool
	// Variants 显式声明本开关所有合法变体名。
	// OffVariant、定向规则变体、放量变体都必须在其中。
	Variants []string
	// OffVariant 是开关关闭或前置条件不满足时返回的关闭变体。
	OffVariant string
	// Prerequisites 为有序前置开关；任一前置结果不等于 RequiredVariant
	// 即返回本开关的关闭变体。
	Prerequisites []Prerequisite
	// Targeting 为有序定向规则，取首个条件全部满足的规则。
	Targeting []Rule
	// DefaultRollout 是没有任何定向规则命中时使用的默认放量。
	DefaultRollout Rollout
}

// Prerequisite 声明对另一个开关求值结果的要求。
type Prerequisite struct {
	// Flag 是前置开关名。
	Flag string
	// RequiredVariant 是前置开关必须返回的变体。
	RequiredVariant string
}

// Op 是定向规则条件支持的比较运算。
type Op string

const (
	// OpEqual 要求用户属性（字符串）等于 Value。
	OpEqual Op = "eq"
	// OpIn 要求用户属性（字符串）出现在 Values 中。
	OpIn Op = "in"
)

// Condition 针对单个用户属性的条件。
// 缺少所引用属性一律视为不满足。
type Condition struct {
	Attribute string
	Op        Op
	Value     string
	Values    []string
}

// Rule 是一条定向规则：所有条件都满足时命中。
type Rule struct {
	Name    string
	When    []Condition
	Variant string  // 非空时直接返回该变体
	Rollout Rollout // Variant 为空时使用该放量
}

// Weight 声明放量中一个变体及其权重（桶数，总和须为 10000）。
type Weight struct {
	Variant string
	Weight  int
}

// Rollout 描述一次百分比放量。
type Rollout struct {
	Weights []Weight
}

// Reason 描述求值命中原因，供结果与日志使用。
type Reason string

const (
	// ReasonDisabled 开关未启用。
	ReasonDisabled Reason = "disabled"
	// ReasonPrerequisiteFailed 某个前置开关结果不满足要求。
	ReasonPrerequisiteFailed Reason = "prerequisite_failed"
	// ReasonTargeting 命中某条定向规则且规则直接指定变体。
	ReasonTargeting Reason = "targeting_rule"
	// ReasonTargetingRollout 命中某条定向规则并走规则放量。
	ReasonTargetingRollout Reason = "targeting_rule_rollout"
	// ReasonDefaultRollout 走默认放量。
	ReasonDefaultRollout Reason = "default_rollout"
)

// EvalResult 是一次求值的结果。
type EvalResult struct {
	// Flag 是被求值的开关名。
	Flag string
	// Variant 是最终命中的变体。
	Variant string
	// Version 是本次求值所基于的规则集版本号。
	Version int64
	// Reason 是命中原因。
	Reason Reason
	// MatchedRule 是命中的定向规则名（命中定向规则时非空）。
	MatchedRule string
	// Bucket 是放量时计算出的 0..9999 桶号；未放量为 -1。
	Bucket int
}
