// Package window 实现带水位线的累积窗口计数器。
//
// 大窗口按固定长度 WindowSize 划分，左闭右开：[start, start+WindowSize)。
// 每个大窗口按步长 Step 细分为若干子窗口终点，事件计入其所属大窗口内
// 所有终点晚于事件时间的子窗口；子窗口到期时输出从大窗口起点到该子窗口
// 终点的累计计数。
package window

import "errors"

// 可区分的拒绝原因。
var (
	ErrEmptyKey          = errors.New("window: empty key")
	ErrInvalidWindowSize = errors.New("window: window size must be positive")
	ErrInvalidStep       = errors.New("window: step must be positive")
	ErrStepNotDivisor    = errors.New("window: window size must be a multiple of step")
	ErrInvalidMaxWindows = errors.New("window: max windows must be positive")
	ErrTooManyWindows    = errors.New("window: retained big windows limit exceeded")
)

// Config 是计数器的配置参数。
type Config struct {
	WindowSize int64 // 大窗口长度（时间单位），必须为正
	Step       int64 // 子窗口步长，必须为正且整除 WindowSize
	MaxWindows int   // 每个键同时保留的大窗口数上限，必须为正
}

// Output 是一个到期子窗口的累计计数输出。
type Output struct {
	Key         string
	WindowStart int64 // 所属大窗口起点（含）
	End         int64 // 子窗口终点（不含）
	Count       int64 // 从 WindowStart 到 End 的累计事件数
}

// Snapshot 是某一键（或全局）在某一时刻的一致性状态视图。
type Snapshot struct {
	Watermark int64 // 当前水位线
	Accepted  int64 // 已接受事件总数
	Dropped   int64 // 因晚于水位线被丢弃的事件总数
	Windows   int   // 当前保留的大窗口数
}

// Counter 是带水位线的累积窗口计数器，可并发使用。
type Counter struct {
	cfg Config
}

// NewCounter 校验配置并创建计数器；配置非法时返回可区分的错误。
func NewCounter(cfg Config) (*Counter, error) {
	return nil, ErrInvalidWindowSize
}

// Add 向计数器加入一个事件。
//
// 返回本次水位线推进所触发的子窗口输出（按 WindowStart、End 升序）。
// 事件因晚于水位线被丢弃时返回 nil 输出与 nil 错误；
// 键为空或保留窗口数超限时返回错误，且计数器状态完全不变。
func (c *Counter) Add(key string, ts int64) ([]Output, error) {
	return nil, ErrEmptyKey
}

// Snapshot 返回指定键当前状态的一致性快照。
func (c *Counter) Snapshot(key string) Snapshot {
	return Snapshot{}
}
