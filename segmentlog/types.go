// Package segmentlog 实现按段（segment）滚动的追加式日志，并提供基于
// 时间与总大小的两阶段保留策略。删除的最小单位是整段：保留执行后剩余段
// 依旧首尾相接、构成连续可读区间，起始位点只进不退，读取结果可复现。
package segmentlog

import "time"

// 错误类别：彼此互不相同，可用 errors.Is 精确区分。非法输入在任何状态
// 变更之前被整体拒绝，因此失败不会在日志上留下痕迹。
var (
	// ErrInvalidConfig 表示保留策略或段容量配置非法。
	ErrInvalidConfig = newError("segmentlog: invalid configuration")
	// ErrInvalidRecord 表示待追加记录非法（无时间戳、空数据、字节数不匹配等）。
	ErrInvalidRecord = newError("segmentlog: invalid record")
	// ErrClockBackwards 表示记录/保留时间早于已接受的最大时间戳。
	ErrClockBackwards = newError("segmentlog: timestamp moved backwards")
	// ErrOutOfRange 表示读取区间为空、越界或未落在记录边界上。
	ErrOutOfRange = newError("segmentlog: read range out of bounds")
)

// Config 控制段滚动与保留策略。零值字段表示关闭对应保留维度。
type Config struct {
	// MaxSegmentBytes 为单个活动段的容量上限；追加后若超出则滚动新段。
	MaxSegmentBytes int64
	// MaxAge 为时间保留窗口；相对当前时钟，保留时间更早的整段被删除。
	// 取 0 表示关闭时间阶段。
	MaxAge time.Duration
	// MaxTotalBytes 为保留总字节数上限；删除最旧整段直到不超过该值。
	// 取 0 表示关闭大小阶段。
	MaxTotalBytes int64
}

// Record 是一条待追加记录。Bytes 必须等于 len(Data) 且为正数。
type Record struct {
	Time  time.Time
	Data  []byte
	Bytes int64
}

// SegmentInfo 为某一段在某一时刻的只读快照。
type SegmentInfo struct {
	ID        int
	StartOff  int64
	EndOff    int64
	Bytes     int64
	FirstTime time.Time
	LastTime  time.Time
	RecordNum int
	Active    bool
}

// Decision 记录保留判定中某一阶段对某一段的结论。
type Decision struct {
	Phase   string // "time" 或 "size"
	Segment SegmentInfo
	Delete  bool
	Reason  string
}

// Report 描述一次 Retain 的完整判定过程，供测试与调试日志复用。
type Report struct {
	Now               time.Time
	TimePhase         []Decision
	SizePhase         []Decision
	Deleted           []SegmentInfo
	StartOffsetBefore int64
	StartOffsetAfter  int64
	TotalBefore       int64
	TotalAfter        int64
}
