package scheduler

import "errors"

// RejectReason 标识一次输入被整体拒绝的具体原因。
type RejectReason string

const (
	ReasonNilTransactions     RejectReason = "nil_transactions"
	ReasonEmptyTransactions   RejectReason = "empty_transactions"
	ReasonSequenceGap         RejectReason = "sequence_gap"
	ReasonEmptyWriteSet       RejectReason = "empty_write_set"
	ReasonEmptyWriteKey       RejectReason = "empty_write_key"
	ReasonTooManyTransactions RejectReason = "too_many_transactions"
	ReasonInvalidParallelism  RejectReason = "invalid_parallelism"
)

// RejectError 携带可区分的拒绝原因与可读说明。
type RejectError struct {
	Reason  RejectReason
	Message string
}

func (e *RejectError) Error() string {
	return "scheduler: " + string(e.Reason) + ": " + e.Message
}

func reject(reason RejectReason, message string) error {
	return &RejectError{Reason: reason, Message: message}
}

// RejectReasonOf 返回错误对应的拒绝原因；非拒绝错误返回空串。
func RejectReasonOf(err error) RejectReason {
	var target *RejectError
	if errors.As(err, &target) {
		return target.Reason
	}
	return ""
}
