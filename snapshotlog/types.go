// Package snapshotlog 实现“快照 + 增量日志”衔接组件。
//
// 源表的每次写入都会追加到一条带连续序号的日志；对一个键范围按
// “记低水位 → 读快照 → 记高水位并修正快照”三步建立基线，之后轮询
// 只把高水位之后、且落在已完成范围内的日志按序应用到下游视图，
// 保证视图与源表一致、输出不重复、不回退。
package snapshotlog

import (
	"errors"
	"log/slog"
)

// Op 是日志操作类型。
type Op int

const (
	// OpPut 表示写入（覆盖）一个键。
	OpPut Op = iota + 1
	// OpDelete 表示删除一个键。
	OpDelete
)

// String 返回操作的可读名称，用于日志输出。
func (o Op) String() string {
	switch o {
	case OpPut:
		return "PUT"
	case OpDelete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// Entry 是日志中的一条记录。Seq 从 1 开始且全局连续、严格递增。
type Entry struct {
	Seq   int64
	Op    Op
	Key   int64
	Value string // OpDelete 时为空
}

// KeyRange 是闭区间 [Start, End]，边界键属于该范围。
type KeyRange struct {
	Start int64
	End   int64
}

// Valid 判断键范围是否合法（Start <= End）。
func (r KeyRange) Valid() bool { return r.Start <= r.End }

// Contains 判断键 k 是否落在范围内（含边界）。
func (r KeyRange) Contains(k int64) bool { return r.Start <= k && k <= r.End }

// Overlaps 判断两个闭区间是否相交（边界键相同也算相交）。
func (r KeyRange) Overlaps(o KeyRange) bool {
	return r.Contains(o.Start) || r.Contains(o.End) || o.Contains(r.Start)
}

func (r KeyRange) String() string {
	return "[" + itoa(r.Start) + "," + itoa(r.End) + "]"
}

// 可区分的拒绝原因：调用方可用 errors.Is 判定。
var (
	// ErrInvalidRange 键范围非法（Start > End）。
	ErrInvalidRange = errors.New("snapshotlog: 键范围非法 (start > end)")
	// ErrRangeOverlap 键范围与已有（进行中或已完成）范围相交。
	ErrRangeOverlap = errors.New("snapshotlog: 键范围与已有范围相交")
	// ErrPhaseOrder 阶段顺序错误（未先开始就读快照/完成、重复进入某阶段等）。
	ErrPhaseOrder = errors.New("snapshotlog: 阶段顺序错误")
	// ErrViewLimitExceeded 视图行数将超过上限。
	ErrViewLimitExceeded = errors.New("snapshotlog: 视图行数超过上限")
)

// AppliedEvent 是轮询时真正应用到视图的一条增量输出。
type AppliedEvent struct {
	Entry Entry
}

// Option 配置 Syncer。
type Option func(*config)

type config struct {
	logger      *slog.Logger
	maxViewRows int // <=0 表示不限
}

func defaultConfig() config {
	return config{
		logger:      slog.Default(),
		maxViewRows: 0,
	}
}

// WithLogger 注入日志记录器；日志中打印输入、输出与判定依据。
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithMaxViewRows 限制视图中存活键的最大行数；n<=0 表示不限。
func WithMaxViewRows(n int) Option {
	return func(c *config) { c.maxViewRows = n }
}

// itoa 是一个不依赖 strconv 的最小整数格式化，供 KeyRange.String 使用。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
