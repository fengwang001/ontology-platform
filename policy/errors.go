package policy

// Code 是可区分的拒绝原因码，次序即规格中固定的拒绝优先级。
type Code int

const (
	CodeInvalid Code = iota + 1 // 参数非法
	CodePolicyMissing
	CodeClockBack
	CodeTerminated
	CodeEndMissing
	CodeEndDuplicate
	CodeEndEffective
	CodeRetroactive
	CodeAwaitingPay
	CodePendingEnds
	CodeAlreadyPaid
	CodeBelowMinSA
)

// EngineError 携带可区分的错误码，errors.Is 可直接比较哨兵错误。
type EngineError struct{ code Code }

func (e *EngineError) Error() string {
	switch e.code {
	case CodeInvalid:
		return "参数非法"
	case CodePolicyMissing:
		return "保单不存在"
	case CodeClockBack:
		return "时钟回退"
	case CodeTerminated:
		return "已终态"
	case CodeEndMissing:
		return "批改不存在"
	case CodeEndDuplicate:
		return "批改重复"
	case CodeEndEffective:
		return "已生效"
	case CodeRetroactive:
		return "追溯批改"
	case CodeAwaitingPay:
		return "待补缴"
	case CodePendingEnds:
		return "存在未生效批改"
	case CodeAlreadyPaid:
		return "已缴"
	case CodeBelowMinSA:
		return "低于最低保额"
	default:
		return "未知错误"
	}
}

// Code 返回错误码。
func (e *EngineError) Code() Code { return e.code }

func (e *EngineError) Is(target error) bool {
	t, ok := target.(*EngineError)
	return ok && t.code == e.code
}

var (
	ErrInvalid       = &EngineError{CodeInvalid}
	ErrPolicyMissing = &EngineError{CodePolicyMissing}
	ErrClockBack     = &EngineError{CodeClockBack}
	ErrTerminated    = &EngineError{CodeTerminated}
	ErrEndMissing    = &EngineError{CodeEndMissing}
	ErrEndDuplicate  = &EngineError{CodeEndDuplicate}
	ErrEndEffective  = &EngineError{CodeEndEffective}
	ErrRetroactive   = &EngineError{CodeRetroactive}
	ErrAwaitingPay   = &EngineError{CodeAwaitingPay}
	ErrPendingEnds   = &EngineError{CodePendingEnds}
	ErrAlreadyPaid   = &EngineError{CodeAlreadyPaid}
	ErrBelowMinSA    = &EngineError{CodeBelowMinSA}
)
