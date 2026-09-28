// Package hopping 实现跳跃（滑动步长）窗口计数器。
package hopping

import "errors"

// 可区分的拒绝原因。调用方可用 errors.Is 判定具体原因。
var (
	// ErrInvalidConfig 表示计数器配置非法（步长/窗长/容量不合法）。
	ErrInvalidConfig = errors.New("hopping: invalid config")

	// ErrEmptyKey 表示事件键为空字符串。
	ErrEmptyKey = errors.New("hopping: empty key")

	// ErrInvalidCount 表示事件计数增量非正。
	ErrInvalidCount = errors.New("hopping: count must be positive")

	// ErrInvalidTimestamp 表示时间戳超出可安全表示的范围（窗口端点会溢出）。
	ErrInvalidTimestamp = errors.New("hopping: timestamp out of representable range")

	// ErrClockRewind 表示推进时钟时传入的水位早于当前水位（时钟只进不退）。
	ErrClockRewind = errors.New("hopping: clock cannot move backwards")

	// ErrTooManyOpenWindows 表示该事件需要同时保留的打开窗口数将超过上限。
	ErrTooManyOpenWindows = errors.New("hopping: too many open windows")
)
