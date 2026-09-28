// Package segmentlog 实现分段日志及其保留策略。
//
// 日志由若干首尾相接的段组成：每条记录追加到当前活动段；活动段容量不足时
// 整段封闭并新建活动段。保留策略按时间与总大小两阶段、自最旧段起逐段删除，
// 删除的最小单位是整段，活动段永不删除。
package segmentlog

// Config 描述日志容量与保留配置。
type Config struct {
	// MaxSegmentBytes 单个段可容纳记录字节数之和的上限，必须 > 0。
	MaxSegmentBytes int64
	// MaxTotalBytes 所有段字节数之和的上限，必须 > 0 且 >= MaxSegmentBytes。
	MaxTotalBytes int64
	// MaxAge 保留时长（纳秒）。段的保留时间取段内记录时间戳的最大值；
	// 当 now - 段保留时间 > MaxAge 时该段可删除。必须 > 0。
	MaxAge int64
}

// Record 是一条日志记录。
type Record struct {
	// Timestamp 记录时间戳（单调递增的纳秒计数即可），必须 > 0。
	Timestamp int64
	// Payload 记录内容，长度即其字节数；不允许空记录。
	Payload []byte
}

// SegmentInfo 描述一个段的只读视图，用于快照与日志输出。
type SegmentInfo struct {
	// Index 段在当前日志中的序号（从 0 起，随删除前移而重排）。
	Index int
	// StartOffset 段首记录之前已确认的总字节数（段起始位点）。
	StartOffset int64
	// Bytes 段内记录字节数之和。
	Bytes int64
	// MinTimestamp 段内最小记录时间戳。
	MinTimestamp int64
	// MaxTimestamp 段内最大记录时间戳，即段的保留时间。
	MaxTimestamp int64
	// Records 段内记录条数。
	Records int
	// Active 是否为当前活动段。
	Active bool
}

// RetainDecision 记录一次保留判定中单个段的判定依据。
type RetainDecision struct {
	Segment SegmentInfo
	// TimeReason 时间阶段判定说明。
	TimeReason string
	// TimeDeleted 是否在时间阶段被判定删除。
	TimeDeleted bool
	// SizeReason 大小阶段判定说明（时间阶段已删除的段不再进入大小阶段）。
	SizeReason string
	// SizeDeleted 是否在大小阶段被判定删除。
	SizeDeleted bool
	// FinalDeleted 该段最终是否被删除。
	FinalDeleted bool
}

// RetainResult 是一次 Retain 调用的完整结果。
type RetainResult struct {
	// Now 本次判定使用的当前时间。
	Now int64
	// DeletedSegments 被整段删除的段（按旧到新排序）。
	DeletedSegments []SegmentInfo
	// DeletedBytes 被删除的字节总数。
	DeletedBytes int64
	// StartOffset 判定完成后新的日志起始位点。
	StartOffset int64
	// TotalBytes 判定完成后剩余总字节数。
	TotalBytes int64
	// Decisions 每个段的逐条判定依据（含被删与保留的段）。
	Decisions []RetainDecision
}

// 各类可区分的拒绝原因。
var (
	// ErrInvalidConfig 配置非法（零值、负值或总上限小于段上限）。
	ErrInvalidConfig = invalidConfigError{}
	// ErrInvalidRecord 记录非法（空内容或字节数为 0）。
	ErrInvalidRecord = invalidRecordError{}
	// ErrInvalidTimestamp 时间戳非法（非正数）。
	ErrInvalidTimestamp = invalidTimestampError{}
	// ErrClockBackwards 时间戳相对上一条记录发生回退。
	ErrClockBackwards = clockBackwardsError{}
	// ErrOffsetOutOfRange 读取起始位点不在可读区间内。
	ErrOffsetOutOfRange = offsetOutOfRangeError{}
)

type invalidConfigError struct{}

func (invalidConfigError) Error() string { return "segmentlog: invalid config" }

type invalidRecordError struct{}

func (invalidRecordError) Error() string { return "segmentlog: invalid record" }

type invalidTimestampError struct{}

func (invalidTimestampError) Error() string { return "segmentlog: invalid timestamp" }

type clockBackwardsError struct{}

func (clockBackwardsError) Error() string { return "segmentlog: clock moved backwards" }

type offsetOutOfRangeError struct{}

func (offsetOutOfRangeError) Error() string { return "segmentlog: read offset out of range" }

// Log 是并发安全的分段日志。零值不可用，必须通过 New 创建。
type Log struct {
	// 骨架阶段为空实现。
}

// New 校验配置并创建空日志。空日志的起始位点为 0。
func New(cfg Config) (*Log, error) {
	return nil, nil
}

// Append 追加一条记录。若当前活动段已满则先整段封闭、新建活动段再写入。
// 任何校验失败都不会改变日志状态。
func (l *Log) Append(now int64, payload []byte) (offset int64, err error) {
	return 0, nil
}

// Retain 依据配置执行两阶段保留（先时间、后大小），
// 返回完整判定依据；日志起始位点随删除前移。
func (l *Log) Retain(now int64) (RetainResult, error) {
	return RetainResult{}, nil
}

// Read 从 offset 起顺序读取至多 max 条记录。
// offset 必须落在 [StartOffset, StartOffset+TotalBytes] 内。
func (l *Log) Read(offset int64, max int) ([]Record, error) {
	return nil, nil
}

// StartOffset 返回当前日志起始位点（只进不退）。
func (l *Log) StartOffset() int64 { return 0 }

// TotalBytes 返回当前所有段字节数之和。
func (l *Log) TotalBytes() int64 { return 0 }

// Snapshot 返回当前段列表的只读快照（旧到新，最后一段为活动段）。
func (l *Log) Snapshot() []SegmentInfo { return nil }
