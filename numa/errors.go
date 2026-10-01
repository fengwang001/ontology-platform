package numa

// RejectReason 描述一次 Admit / Release / Query 被拒绝的首个原因。
type RejectReason string

const (
	ReasonInvalidConfig    RejectReason = "invalid-config"
	ReasonInvalidArgument  RejectReason = "invalid-argument"
	ReasonContainerExists  RejectReason = "container-exists"
	ReasonInsufficientCPU  RejectReason = "insufficient-cpu"
	ReasonTooManyCombos    RejectReason = "too-many-combinations"
	ReasonHintNotSatisfied RejectReason = "hint-not-satisfied"
	ReasonContainerMissing RejectReason = "container-missing"
)

// RejectError 携带可区分的拒绝原因。
type RejectError struct {
	Reason RejectReason
	msg    string
}

func (e *RejectError) Error() string { return string(e.Reason) + ": " + e.msg }

func reject(reason RejectReason, msg string) error {
	return &RejectError{Reason: reason, msg: msg}
}
