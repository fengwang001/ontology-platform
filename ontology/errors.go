package ontology

// 本文件定义脱敏过程中所有可区分的拒绝原因。

import "fmt"

// Reason 标识一次整事件被拒绝的具体类别。
type Reason string

const (
	// ReasonInvalidConfig 配置本身非法（重复表名、空域名/表名/列名、限额非法等）。
	ReasonInvalidConfig Reason = "invalid_config"
	// ReasonUnknownTable 事件引用了配置中不存在的表。
	ReasonUnknownTable Reason = "unknown_table"
	// ReasonInvalidEvent 事件形状非法（nil、缺表名、镜像不是列映射、列值类型不支持等）。
	ReasonInvalidEvent Reason = "invalid_event"
	// ReasonTokenLimitExceeded 某域的令牌表超过配置的上限。
	ReasonTokenLimitExceeded Reason = "token_limit_exceeded"
)

// RejectError 描述一次被整体拒绝的脱敏调用。
type RejectError struct {
	Reason Reason
	detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("tokenize rejected (%s): %s", e.Reason, e.detail)
}

func rejectf(reason Reason, format string, args ...any) error {
	return &RejectError{Reason: reason, detail: fmt.Sprintf(format, args...)}
}

// Is 让 errors.Is(err, ErrInvalidConfig) 等按 Reason 匹配。
func (e *RejectError) Is(target error) bool {
	t, ok := target.(*RejectError)
	return ok && t.Reason == e.Reason
}

// 各类别的哨兵错误，便于调用方按类别判定。
var (
	ErrInvalidConfig      = &RejectError{Reason: ReasonInvalidConfig}
	ErrUnknownTable       = &RejectError{Reason: ReasonUnknownTable}
	ErrInvalidEvent       = &RejectError{Reason: ReasonInvalidEvent}
	ErrTokenLimitExceeded = &RejectError{Reason: ReasonTokenLimitExceeded}
)
