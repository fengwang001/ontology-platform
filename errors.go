package shophours

// ErrorCode 以程序化方式区分各类被拒绝的操作。
type ErrorCode int

const (
	OK ErrorCode = iota
	ErrInvalidParam
	ErrClockRollback
	ErrNotFound
	ErrState
	ErrForcedSuspension
	ErrTemporaryClosure
	ErrClosureGap
	ErrClosureOverlap
	ErrPendingOrders
	ErrNotOpen
	ErrNearClosing
	ErrReservationTooFar
)

// BizError 是本系统所有拒绝操作返回的错误类型。
type BizError struct {
	Code ErrorCode
	Msg  string
}

func (e *BizError) Error() string { return e.Msg }

func bizErr(code ErrorCode, msg string) *BizError {
	return &BizError{Code: code, Msg: msg}
}
