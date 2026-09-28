// Package counter 实现带水位线的累积窗口计数器。
//
// 时间轴被划分为固定长度、左闭右开的大窗口 [start, start+size)；
// 每个大窗口内部再按固定步长切出若干子窗口终点。事件按其时间戳归入
// 唯一的大窗口，并被计入该大窗口内所有终点晚于事件时间的子窗口，因此
// 每次触发输出的都是从大窗口起点到当前子窗口终点的累计计数。
package counter

import (
	"io"
)

// 拒绝原因码，可通过 RejectError.Code 或 errors.Is 区分被拒绝的具体原因。
const (
	// ReasonInvalidConfig：构造参数非法（长度、步长、上限不合法等）。
	ReasonInvalidConfig = "invalid_config"
	// ReasonEmptyKey：事件的键为空字符串。
	ReasonEmptyKey = "empty_key"
	// ReasonTooManyWindows：事件落入的大窗口导致同时保留的大窗口数超过上限。
	ReasonTooManyWindows = "too_many_windows"
)

// 哨兵错误，便于调用方用 errors.Is 判定拒绝原因。
var (
	ErrInvalidConfig  = &RejectError{Code: ReasonInvalidConfig, Reason: "invalid counter config"}
	ErrEmptyKey       = &RejectError{Code: ReasonEmptyKey, Reason: "event key must not be empty"}
	ErrTooManyWindows = &RejectError{Code: ReasonTooManyWindows, Reason: "too many retained big windows"}
)

// RejectError 描述一次被拒绝的输入及其可区分的原因码。
type RejectError struct {
	Code   string // 拒绝原因码，取值为 Reason* 常量
	Reason string // 人类可读的原因说明
}

func (e *RejectError) Error() string { return e.Code + ": " + e.Reason }

// Is 使包装后的错误仍能通过 errors.Is 匹配到对应哨兵。
func (e *RejectError) Is(target error) bool {
	t, ok := target.(*RejectError)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// Config 是计数器的构造参数。
type Config struct {
	// WindowSize 是大窗口的固定长度，必须为正数。
	WindowSize int64
	// Step 是大窗口内子窗口终点之间的固定步长，必须为正数，
	// 且必须整除 WindowSize（即 WindowSize%Step == 0）。
	Step int64
	// MaxWindows 是同一时刻允许保留（活跃）的大窗口数量上限，必须为正数。
	MaxWindows int
}

// Event 是一条输入事件：键 Key 在时间戳 Timestamp 时刻产生一次计数。
type Event struct {
	Key       string
	Timestamp int64 // 可为负数；大窗口划分对负时间戳同样成立
}

// Result 是一次到期触发的输出：在 Key 对应的大窗口 [WindowStart, WindowStart+size)
// 内，时间戳落在 [WindowStart, End) 中的事件累计个数。
type Result struct {
	Key         string
	WindowStart int64 // 所属大窗口起点
	End         int64 // 子窗口终点（累计区间右端，左闭右开）
	Count       int64 // 从大窗口起点到 End 的累计计数（为零也会输出）
}

// State 是计数器某一时刻的可并发读取快照，字段逐字段一致。
type State struct {
	// Watermark 是当前水位线（已接收的最大事件时间，单调不减）。
	// 尚无任何被接收事件时为 math.MinInt64（表示“尚未设置”）。
	Watermark int64
	// WatermarkSet 表示水位线是否已被至少一条被接收事件设置过。
	WatermarkSet bool
	Dropped      int64    // 因最小子窗口终点不超过水位线而被丢弃的事件数
	Outputs      []Result // 截至目前已触发输出的全部结果（深拷贝，按确定顺序排列）
}

// Option 配置 Counter 的可选行为。
type Option func(*options)

type options struct {
	logger io.Writer
}

// WithLogger 注入日志输出目标；日志包含每条输入、每个输出及判定依据。
// 不传时默认写入 log.Default() 的输出。
func WithLogger(w io.Writer) Option {
	return func(o *options) { o.logger = w }
}
