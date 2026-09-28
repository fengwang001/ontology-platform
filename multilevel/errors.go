package multilevel

import "fmt"

// RejectError 表示一次增量被整体拒绝。不同 ReasonCode 互不相同、可区分，
// 可用 errors.As 提取 Reason 判定类别。
type RejectError struct {
	Reason ReasonCode
	msg    string
}

func (e *RejectError) Error() string { return fmt.Sprintf("%s: %s", e.Reason, e.msg) }

func rejectf(reason ReasonCode, format string, args ...any) *RejectError {
	return &RejectError{Reason: reason, msg: fmt.Sprintf(format, args...)}
}
