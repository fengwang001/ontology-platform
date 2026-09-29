package pkchange

import "errors"

// RejectReason 标识批被拒绝的可区分原因。
type RejectReason string

const (
	ReasonInvalidKey    RejectReason = "invalid_key"     // 主键非法
	ReasonKeyExists     RejectReason = "key_exists"      // 插入/更新目标键已存在
	ReasonKeyNotFound   RejectReason = "key_not_found"   // 更新/删除的键不存在
	ReasonBatchTooLarge RejectReason = "batch_too_large" // 批内行数超限
	ReasonInvalidChange RejectReason = "invalid_change"  // 变更本身不合法（未知操作等）
)

// RejectError 表示整批变更被拒绝；批内任何状态都不会改变。
type RejectError struct {
	Reason  RejectReason
	Index   int    // 批中触发拒绝的变更下标
	Key     string // 相关主键（可能为空）
	Message string
	Limit   int
}

func (e *RejectError) Error() string { return e.Message }

// AsReject 从 error 中提取 *RejectError。
func AsReject(err error) (*RejectError, bool) {
	var r *RejectError
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}
