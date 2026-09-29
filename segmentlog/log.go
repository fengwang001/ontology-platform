// Package segmentlog 提供按段组织、按时间与总大小定期清理旧数据的追加型日志。
//
// 删除的最小单位是整段：保留判定从最旧段起逐段进行，遇到第一个不满足删除
// 条件的段即停止，活动段永不删除。删除后剩余段仍然首尾相接，起始位点只进
// 不退，读取得到的记录内容与追加时完全一致。
package segmentlog

import (
	"context"
	"sync"
)

// Record 是一条日志记录：Time 为时间戳（Unix 毫秒，必须为正且单调不减），
// Data 为记录内容；其字节数按 len(Data) 计。
type Record struct {
	Time int64
	Data []byte
}

// Config 是分段日志的保留配置。
type Config struct {
	// MaxSegmentBytes 为单个活动段的容量上限；追加后超过该值即滚动新段。
	MaxSegmentBytes int64
	// MaxTotalBytes 为全部段总字节数的软上限；超出时从最旧的非活动段起逐段删除。
	MaxTotalBytes int64
	// RetentionMillis 为时间保留窗口；段保留时间（段内最大时间戳）早于
	// now-RetentionMillis 的最旧段会被逐段删除。为 0 表示不按时间删除。
	RetentionMillis int64
}

// Log 是并发安全的分段日志。
type Log struct {
	cfg Config

	mu            sync.RWMutex
	segs          []*segment
	startOffset   int64 // 最早一条尚存记录的起始位点；只进不退
	nextOffset    int64 // 下一条记录的起始位点
	totalBytes    int64 // 全部剩余段字节数
	lastAppendAt  int64 // 已接受记录的最大时间戳，用于检测时钟回退
	lastRetainNow int64 // 上一次保留判定的时钟，用于检测时钟回退
}

type entry struct {
	time int64
	data []byte
}

type segment struct {
	startOffset int64 // 段内首条记录的起始位点
	endOffset   int64 // 段尾位点（下一段的起始位点，首尾相接）
	bytes       int64 // 段内字节总数
	maxTime     int64 // 段内记录时间戳的最大值，即段的保留时间
	records     []entry
}

// SegmentInfo 描述一个段的对外可见状态。
type SegmentInfo struct {
	Index       int
	StartOffset int64
	EndOffset   int64
	Bytes       int64
	MaxTime     int64
	Records     int
	Active      bool
}

// Step 描述保留判定过程中的一步，用于可复现地说明判定依据。
type Step struct {
	Phase       string
	Action      string
	Segment     SegmentInfo
	Reason      string
	StartOffset int64
	TotalBytes  int64
}

// New 创建一个分段日志；配置非法时返回对应错误且不分配任何状态。
func New(cfg Config) (*Log, error) {
	if cfg.MaxSegmentBytes <= 0 {
		return nil, reject(ErrInvalidConfig, "MaxSegmentBytes must be positive, got %d", cfg.MaxSegmentBytes)
	}
	if cfg.MaxTotalBytes <= 0 {
		return nil, reject(ErrInvalidConfig, "MaxTotalBytes must be positive, got %d", cfg.MaxTotalBytes)
	}
	if cfg.MaxTotalBytes < cfg.MaxSegmentBytes {
		return nil, reject(ErrInvalidConfig,
			"MaxTotalBytes (%d) must be >= MaxSegmentBytes (%d)", cfg.MaxTotalBytes, cfg.MaxSegmentBytes)
	}
	if cfg.RetentionMillis < 0 {
		return nil, reject(ErrInvalidConfig, "RetentionMillis must be non-negative, got %d", cfg.RetentionMillis)
	}
	return &Log{cfg: cfg}, nil
}

// Append 追加一条记录；活动段已满时先滚动新段。任何非法输入都整体拒绝且不留痕。
// 返回该记录在日志中的起始位点。
func (l *Log) Append(ctx context.Context, rec Record) (offset int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	size := int64(len(rec.Data))
	if rec.Time <= 0 {
		return 0, reject(ErrInvalidRecord, "record timestamp must be positive, got %d", rec.Time)
	}
	if size == 0 {
		return 0, reject(ErrInvalidRecord, "record data must not be empty")
	}
	if size > l.cfg.MaxSegmentBytes {
		return 0, reject(ErrInvalidRecord,
			"record size %d exceeds MaxSegmentBytes %d", size, l.cfg.MaxSegmentBytes)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if rec.Time < l.lastAppendAt {
		return 0, reject(ErrClockBackwards,
			"record timestamp %d is earlier than last accepted timestamp %d", rec.Time, l.lastAppendAt)
	}

	// 复制一份数据，确保调用方事后修改切片不会改变日志内容（可复现）。
	stored := make([]byte, size)
	copy(stored, rec.Data)

	if len(l.segs) == 0 || l.segs[len(l.segs)-1].bytes+size > l.cfg.MaxSegmentBytes {
		l.segs = append(l.segs, &segment{startOffset: l.nextOffset, endOffset: l.nextOffset})
	}
	active := l.segs[len(l.segs)-1]

	offset = l.nextOffset
	active.records = append(active.records, entry{time: rec.Time, data: stored})
	active.bytes += size
	active.endOffset = l.nextOffset + size
	if rec.Time > active.maxTime {
		active.maxTime = rec.Time
	}
	l.nextOffset += size
	l.totalBytes += size
	l.lastAppendAt = rec.Time
	return offset, nil
}

// StartOffset 返回当前日志起始位点；它只进不退。
func (l *Log) StartOffset() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.startOffset
}

// TotalBytes 返回全部剩余段的字节总数。
func (l *Log) TotalBytes() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.totalBytes
}
