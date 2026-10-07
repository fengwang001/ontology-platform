package ontology

// RejectCode 标识一次申请/更新被拒绝的具体原因，四类拒绝互斥。
type RejectCode int

const (
	// RejectNone 表示未拒绝（成功）。
	RejectNone RejectCode = iota
	// RejectOccupiedByAction 占用申请被拒绝：实例已被另一动作占用。
	RejectOccupiedByAction
	// RejectInstanceOccupied 占用期间的普通乐观更新被拒绝。
	RejectInstanceOccupied
	// RejectVersionStale 乐观更新因版本落后被拒绝。
	RejectVersionStale
	// RejectOrderConflict 跨实例占用申请违反确定性顺序规则被拒绝。
	RejectOrderConflict
	// RejectCardinality 属性基数约束冲突。
	RejectCardinality
	// RejectOccupancyLost 持有方租约失效后仍尝试提交（栅栏令牌不匹配）。
	RejectOccupancyLost
)

func (c RejectCode) String() string {
	switch c {
	case RejectNone:
		return "none"
	case RejectOccupiedByAction:
		return "occupied_by_action"
	case RejectInstanceOccupied:
		return "instance_occupied"
	case RejectVersionStale:
		return "version_stale"
	case RejectOrderConflict:
		return "order_conflict"
	case RejectCardinality:
		return "cardinality_conflict"
	case RejectOccupancyLost:
		return "occupancy_lost"
	}
	return "unknown"
}

// RejectError 是可单独识别的拒绝结果。
type RejectError struct {
	Code    RejectCode
	Message string
}

func (e *RejectError) Error() string { return e.Code.String() + ": " + e.Message }

func reject(code RejectCode, msg string) *RejectError {
	return &RejectError{Code: code, Message: msg}
}
