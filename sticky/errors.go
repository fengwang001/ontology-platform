package sticky

import "errors"

// 非法输入的错误类别。整批校验失败时 BatchError.Reason 取其中之一，
// 彼此互不相同，可通过 errors.Is 精确区分。
var (
	// ErrEmptyMemberID：加入操作携带空成员标识。
	ErrEmptyMemberID = errors.New("sticky: member id must not be empty")
	// ErrDuplicateJoin：同一成员在同一批中被重复加入，或加入已在组内的成员。
	ErrDuplicateJoin = errors.New("sticky: duplicate member join in batch")
	// ErrLeaveUnknown：离开操作指向批前不在组内的成员。
	ErrLeaveUnknown = errors.New("sticky: cannot leave: member not in group")
	// ErrMemberLimit：批后成员数超过构造时指定的上限。
	ErrMemberLimit = errors.New("sticky: member count exceeds limit")
)

// BatchError 描述一次被整批拒绝的变更。
type BatchError struct {
	Reason error
	Index  int
	Member string
}

func (e *BatchError) Error() string {
	if e == nil || e.Reason == nil {
		return "sticky: batch rejected"
	}
	return e.Reason.Error()
}

func (e *BatchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Reason
}
