package dbscan

// Reason 标识一次被拒绝操作的可区分原因。
type Reason string

const (
	ReasonInvalidParam Reason = "invalid parameter"
	ReasonIDExists     Reason = "id already alive"
	ReasonCapacityFull Reason = "capacity full"
	ReasonIDNotFound   Reason = "id not alive"
	ReasonClockRewind  Reason = "clock moved backwards"
)

// OpError 携带被拒绝操作的原因。
type OpError struct {
	Reason Reason
	Msg    string
}

func (e *OpError) Error() string { return string(e.Reason) + ": " + e.Msg }

func invalidParam(msg string) error { return &OpError{Reason: ReasonInvalidParam, Msg: msg} }
