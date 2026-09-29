package cdctoken

import "errors"

// Reason 描述一次拒绝的可区分原因。
type Reason string

const (
	// ReasonInvalidConfig 配置非法（空域名、重复域、空表名、未注册的域等）。
	ReasonInvalidConfig Reason = "invalid_config"
	// ReasonUnknownTable 事件涉及未在配置中注册的表。
	ReasonUnknownTable Reason = "unknown_table"
	// ReasonInvalidEvent 事件形状非法（空表名、镜像内同名列冲突等）。
	ReasonInvalidEvent Reason = "invalid_event"
	// ReasonTokenTableExhausted 令牌表超限：本次事件需要的新令牌数会使某域超过上限。
	ReasonTokenTableExhausted Reason = "token_table_exhausted"
)

// RejectError 表示一次被整体拒绝的处理，携带可区分的原因与详细信息。
type RejectError struct {
	Reason Reason
	msg    string
}

func (e *RejectError) Error() string { return string(e.Reason) + ": " + e.msg }

func reject(reason Reason, format string, args ...any) error {
	return &RejectError{Reason: reason, msg: sprintf(format, args...)}
}

// IsReject 判断错误是否为携带指定原因的拒绝错误；reason 为空时匹配任意拒绝错误。
func IsReject(err error, reason Reason) bool {
	var r *RejectError
	if !errors.As(err, &r) {
		return false
	}
	return reason == "" || r.Reason == reason
}
