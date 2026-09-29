package featureflag

import "errors"

// 发布校验错误，按优先级排列；多因同时成立时只报最先出现的一类。
var (
	// ErrUnknownVariant 规则引用了不存在的变体（关闭变体之外未声明）。
	ErrUnknownVariant = errors.New("featureflag: variant referenced but not declared")
	// ErrNegativeWeight 存在负权重。
	ErrNegativeWeight = errors.New("featureflag: rollout weight must not be negative")
	// ErrBadWeightSum 权重之和不等于 10000。
	ErrBadWeightSum = errors.New("featureflag: rollout weights must sum to 10000")
	// ErrUnknownPrerequisite 前置开关不存在。
	ErrUnknownPrerequisite = errors.New("featureflag: prerequisite flag does not exist")
	// ErrPrerequisiteCycle 前置依赖成环。
	ErrPrerequisiteCycle = errors.New("featureflag: prerequisite dependency cycle detected")
)

// PublishError 携带可区分原因的发布错误。
type PublishError struct {
	// Cause 为上述哨兵错误之一。
	Cause error
	// Flag 是触发错误的开关名。
	Flag string
	// Detail 是补充说明（如变体名、环路径）。
	Detail string
}

func (e *PublishError) Error() string {
	msg := e.Cause.Error()
	if e.Flag != "" {
		msg += ": flag=" + e.Flag
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func (e *PublishError) Unwrap() error { return e.Cause }

// ErrUnknownFlag 表示求值了规则集中不存在的开关。
var ErrUnknownFlag = errors.New("featureflag: unknown flag")
