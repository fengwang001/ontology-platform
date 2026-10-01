package capture

import "fmt"

// Reason 描述一次被拒绝操作的可区分原因。
type Reason int

const (
	// ReasonCaptureSlotAboveTop: 捕获槽号不小于栈顶（空槽不能捕获）。
	ReasonCaptureSlotAboveTop Reason = iota + 1
	// ReasonCloseLevelOutOfRange: 关闭层 level 小于 0 或大于栈顶。
	ReasonCloseLevelOutOfRange
	// ReasonSlotOutOfRange: 直接读写栈槽越界（负槽号或不小于栈顶）。
	ReasonSlotOutOfRange
	// ReasonHandleNotFound: 句柄不存在（从未分配）。
	ReasonHandleNotFound
	// ReasonHandleReleased: 句柄已释放完（持有数降到 0）。
	ReasonHandleReleased
)

func (r Reason) String() string {
	switch r {
	case ReasonCaptureSlotAboveTop:
		return "capture-slot-not-below-top"
	case ReasonCloseLevelOutOfRange:
		return "close-level-out-of-range"
	case ReasonSlotOutOfRange:
		return "slot-out-of-range"
	case ReasonHandleNotFound:
		return "handle-not-found"
	case ReasonHandleReleased:
		return "handle-released"
	default:
		return fmt.Sprintf("unknown-reason-%d", int(r))
	}
}

// OpError 携带被拒绝操作的名称与可区分原因。
type OpError struct {
	Op     string
	Reason Reason
}

func (e *OpError) Error() string {
	return fmt.Sprintf("capture: %s rejected: %s", e.Op, e.Reason)
}

func newOpError(op string, reason Reason) *OpError {
	return &OpError{Op: op, Reason: reason}
}
