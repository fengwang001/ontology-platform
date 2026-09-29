package hlc

// RejectReason 标识一次操作被整体拒绝的可区分原因。
type RejectReason int

const (
	ReasonOK RejectReason = iota
	ReasonInvalidArgument
	ReasonNodeNotFound
	ReasonNegativePhysical
	ReasonMessageNotFound
	ReasonMessageAlreadyReceived
	ReasonDriftExceeded
	ReasonCounterOverflow
	ReasonTargetMismatch
)

func (r RejectReason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonInvalidArgument:
		return "invalid_argument"
	case ReasonNodeNotFound:
		return "node_not_found"
	case ReasonNegativePhysical:
		return "negative_physical"
	case ReasonMessageNotFound:
		return "message_not_found"
	case ReasonMessageAlreadyReceived:
		return "message_already_received"
	case ReasonDriftExceeded:
		return "drift_exceeded"
	case ReasonCounterOverflow:
		return "counter_overflow"
	case ReasonTargetMismatch:
		return "target_mismatch"
	default:
		return "unknown"
	}
}

// RejectError 描述一次被整体拒绝的操作及其原因。被拒绝的操作不会改变任何状态。
type RejectError struct {
	Reason RejectReason
	Op     string
	Detail string
}

func (e *RejectError) Error() string {
	if e.Detail == "" {
		return "hlc: " + e.Op + " rejected: " + e.Reason.String()
	}
	return "hlc: " + e.Op + " rejected: " + e.Reason.String() + ": " + e.Detail
}
