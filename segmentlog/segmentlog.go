package segmentlog

import (
	"sync"
	"time"
)

// Record 是一条日志记录。
type Record struct {
	// Timestamp 记录时间戳，必须非零。
	Timestamp time.Time
	// Size 记录占用的字节数，必须为正，且应等于 Data 的长度。
	Size int
	// Data 记录内容，Log 会在写入与读取时复制，避免调用方后续修改造成污染。
	Data []byte
}

// Config 描述分段日志的滚动与保留参数。零值字段表示关闭对应维度。
type Config struct {
	// MaxSegmentBytes 单个段的最大字节数；追加后超出则下一条记录滚入新段。必须 > 0。
	MaxSegmentBytes int
	// MaxAge 段级保留时长：段内最大时间戳早于 now-MaxAge 的段可被整段删除；0 表示不按时间保留。
	MaxAge time.Duration
	// MaxTotalBytes 总字节数上限：超出时从最旧段起整段删除，直到不超限；0 表示不按大小保留。
	MaxTotalBytes int
}

// segment 是不可变的日志段（活动段除外，可继续追加）。
type segment struct {
	id        int
	records   []Record
	bytes     int
	maxTime   time.Time
	startOff  int64
	endOff    int64
}

// Log 是并发安全的分段日志。
type Log struct {
	mu         sync.Mutex
	segs       []*segment
	active     *segment
	totalBytes int64
	startOff   int64
	nextOff    int64
	maxTime    time.Time
	nextSegID  int
	cfg        Config
	logger     Logger
}

// Logger 是日志钩子，默认为标准库 logger；测试可替换为逐行记录器。
type Logger interface {
	Printf(format string, args ...any)
}
