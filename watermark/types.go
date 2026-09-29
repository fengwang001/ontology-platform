package watermark

// Event 是进入双时钟水位维护器的一条事件。
type Event struct {
	// ID 为事件标识，必须非空。
	ID string
	// EventTime 为事件时间，仅它参与迟到判定。
	EventTime int64
}

// RejectReason 标识一次摄入或心跳被整体拒绝的可区分原因。
type RejectReason string

const (
	// RejectNegativeAllowedLateness 表示允许迟到值为负。
	RejectNegativeAllowedLateness RejectReason = "negative_allowed_lateness"
	// RejectEmptyID 表示事件标识为空。
	RejectEmptyID RejectReason = "empty_event_id"
	// RejectHeartbeatRegress 表示处理时间心跳相对当前处理时间水位回退。
	RejectHeartbeatRegress RejectReason = "heartbeat_regression"
)

// RejectError 描述一次失败调用及其可区分原因。
// 任何失败都不得改变两个水位、已接受事件与迟到计数。
type RejectError struct {
	Reason RejectReason
}

func (e *RejectError) Error() string {
	return "watermark: rejected: " + string(e.Reason)
}

// Decision 是一次摄入的判定结果。
type Decision string

const (
	// DecisionAccepted 表示事件按时并被接受。
	DecisionAccepted Decision = "accepted"
	// DecisionLate 表示事件迟到（事件时间不超过事件时间水位）并被丢弃计数。
	DecisionLate Decision = "late"
)

// View 是某一时刻维护器状态的不可变快照。
type View struct {
	// EventWatermark 为事件时间水位；未见过事件时为负无穷。
	EventWatermark int64
	// EventWatermarkNegInf 为 true 时 EventWatermark 字段无意义，表示负无穷。
	EventWatermarkNegInf bool
	// ProcessingWatermark 为处理时间水位，仅由心跳推进、只进不退。
	ProcessingWatermark int64
	// AcceptedEvents 为全部按时接受事件的标识，按标识排序以保证可复现。
	AcceptedEvents []string
	// LateCount 为迟到事件的丢弃计数。
	LateCount int
}

// opKind 标识操作日志中的操作类型，供按序重放使用。
type opKind string

const (
	opIngest    opKind = "ingest"
	opHeartbeat opKind = "heartbeat"
)

// op 是一条按加锁总序记录的操作。
type op struct {
	kind       opKind
	seq        int64
	id         string
	eventTime  int64
	processing int64
}
