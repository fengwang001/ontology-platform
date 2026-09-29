package delegation

import (
	"errors"
	"fmt"
)

// RejectError 表示一次委托操作被拒绝，并携带可区分的原因码。
type RejectError struct {
	Reason RejectReason
	Op     string
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("delegation: %s rejected: %s: %s", e.Op, reasonName(e.Reason), e.Detail)
}

func newReject(op string, reason RejectReason, format string, args ...any) *RejectError {
	return &RejectError{Reason: reason, Op: op, Detail: fmt.Sprintf(format, args...)}
}

// IsReject 判断 err 是否为指定原因的拒绝。
func (e *RejectError) Is(target error) bool {
	re, ok := target.(*RejectError)
	return ok && re.Reason == e.Reason
}

// AsReject 提取错误中的 RejectError。
func AsReject(err error) (*RejectError, bool) {
	var re *RejectError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}

// IsReject 判断 err 是否为指定原因的拒绝。
func IsReject(err error, reason RejectReason) bool {
	re, ok := AsReject(err)
	return ok && re.Reason == reason
}

func reasonName(r RejectReason) string {
	switch r {
	case ReasonNoAuthority:
		return "NO_AUTHORITY"
	case ReasonRedelegateForbidden:
		return "REDELEGATE_FORBIDDEN"
	case ReasonCycle:
		return "CYCLE_DETECTED"
	case ReasonExpired:
		return "ALREADY_EXPIRED"
	case ReasonDuplicate:
		return "DUPLICATE_ID"
	case ReasonSelfDelegation:
		return "SELF_DELEGATION"
	default:
		return "UNKNOWN"
	}
}
