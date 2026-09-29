package scheduler

import "errors"

// Reason 以可区分的枚举值说明操作被拒绝的原因。
type Reason string

const (
	// ReasonInvalidSkew 副本组允许偏斜 S 小于 1。
	ReasonInvalidSkew Reason = "invalid_skew"
	// ReasonInvalidSlots 节点槽位容量非正。
	ReasonInvalidSlots Reason = "invalid_slots"
	// ReasonDuplicateNode 节点标识重复。
	ReasonDuplicateNode Reason = "duplicate_node"
	// ReasonEmptyZone 节点区标签为空。
	ReasonEmptyZone Reason = "empty_zone"
	// ReasonNoMatchingNode 标签要求没有任何节点满足。
	ReasonNoMatchingNode Reason = "no_matching_node"
	// ReasonNoNodeAvailable 所有合格区内都没有满足标签且有空槽的节点。
	ReasonNoNodeAvailable Reason = "no_node_available"
	// ReasonSkewViolated 放到任何有空槽的合格区都会超出允许偏斜。
	ReasonSkewViolated Reason = "skew_violated"
	// ReasonUnknownGroup 引用了未注册的副本组。
	ReasonUnknownGroup Reason = "unknown_group"
	// ReasonDuplicateReplica 副本标识在同一组内已存在。
	ReasonDuplicateReplica Reason = "duplicate_replica"
	// ReasonUnknownReplica 删除的副本不存在。
	ReasonUnknownReplica Reason = "unknown_replica"
	// ReasonAlreadyBound 对已经绑定成功的预留再次绑定。
	ReasonAlreadyBound Reason = "already_bound"
	// ReasonAlreadyReleased 对已释放（绑定失败）的预留再次绑定。
	ReasonAlreadyReleased Reason = "already_released"
	// ReasonUnknownReservation 绑定引用的预留不存在（例如尚未调度）。
	ReasonUnknownReservation Reason = "unknown_reservation"
)

// Error 携带可区分原因码的拒绝错误。
type Error struct {
	Reason Reason
	Op     string
	Msg    string
}

func (e *Error) Error() string {
	return "scheduler: " + e.Op + ": " + string(e.Reason) + ": " + e.Msg
}

func newErr(op string, reason Reason, msg string) *Error {
	return &Error{Reason: reason, Op: op, Msg: msg}
}

// ErrReason 返回错误携带的原因码；非本包错误返回空字符串。
func ErrReason(err error) Reason {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}
