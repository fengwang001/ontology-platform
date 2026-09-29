package softdelete

import "errors"

// Reason 标识操作被拒绝的可区分原因。
type Reason string

const (
	// ReasonNotFound 对象从未存在或已被物理删除。
	ReasonNotFound Reason = "not_found"
	// ReasonAlreadyDeleted 对象已处于软删状态。
	ReasonAlreadyDeleted Reason = "already_deleted"
	// ReasonNotDeleted 对象存活，无法复活。
	ReasonNotDeleted Reason = "not_deleted"
	// ReasonKeyOccupied 唯一键已被存活或软删对象占用。
	ReasonKeyOccupied Reason = "key_occupied"
)

// 可按原因判别的哨兵错误，便于 errors.Is 判断。
var (
	ErrNotFound       = &OpError{Reason: ReasonNotFound}
	ErrAlreadyDeleted = &OpError{Reason: ReasonAlreadyDeleted}
	ErrNotDeleted     = &OpError{Reason: ReasonNotDeleted}
	ErrKeyOccupied    = &OpError{Reason: ReasonKeyOccupied}
)

// OpError 描述一次被拒绝的状态机操作及其原因。
type OpError struct {
	Op     string
	Reason Reason
}

func newOpError(op string, reason Reason) *OpError {
	return &OpError{Op: op, Reason: reason}
}

func (e *OpError) Error() string {
	return "softdelete: " + e.Op + " rejected: " + string(e.Reason)
}

// Is 使 errors.Is 可按 Reason 对应的哨兵错误匹配。
func (e *OpError) Is(target error) bool {
	t, ok := target.(*OpError)
	if !ok {
		return false
	}
	return e.Reason == t.Reason
}

// ReasonOf 返回错误携带的拒绝原因；非本组件错误返回空串。
func ReasonOf(err error) Reason {
	var opErr *OpError
	if errors.As(err, &opErr) {
		return opErr.Reason
	}
	return ""
}
