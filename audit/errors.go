package audit

import "fmt"

// IllegalRequestError 表示参数非法（目标实例不存在、被订正序号不存在或指向订正记录等）。
// 它在错误优先级中排在最前。
type IllegalRequestError struct {
	Reason string
}

func (e *IllegalRequestError) Error() string {
	return "illegal request: " + e.Reason
}

// AuditWriteError 表示审计记录写入失败，动作因此整体回退。
type AuditWriteError struct {
	ActionID string
	Seq      int64
	Cause    error
}

func (e *AuditWriteError) Error() string {
	return fmt.Sprintf("audit write failed for action %q (seq %d reserved): %v", e.ActionID, e.Seq, e.Cause)
}

func (e *AuditWriteError) Unwrap() error { return e.Cause }

// ErrActionRolledBack 是动作逻辑主动要求回退的哨兵错误。
var ErrActionRolledBack = fmt.Errorf("action requested rollback")
