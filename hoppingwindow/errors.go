// Package hoppingwindow 实现跳跃（滑动）窗口计数器。
//
// 窗口按固定滑动步长划分、左闭右开；一个事件同时计入所有包含其时间戳的、
// 尚未关闭的窗口。时钟由调用方通过 Advance 单调推进，推进时关闭所有到期窗口，
// 并按窗口终点、键的顺序输出每个窗口的计数，每个窗口恰好输出一次。
package hoppingwindow

import "fmt"

// ErrorKind 用于区分不同的拒绝原因。
type ErrorKind int

const (
	// KindInvalidConfig：构造参数非法（窗口长度、步长或打开窗口上限）。
	KindInvalidConfig ErrorKind = iota + 1
	// KindEmptyKey：事件键为空字符串。
	KindEmptyKey
	// KindClockRegression：Advance 试图把时钟往回拨。
	KindClockRegression
	// KindTooManyOpenWindows：事件会使同时保留的打开窗口数超过上限。
	KindTooManyOpenWindows
)

// Error 是本包所有可区分拒绝原因的统一错误类型。
type Error struct {
	Kind ErrorKind
	msg  string
}

func (e *Error) Error() string { return e.msg }

// 哨兵错误，可用 errors.Is 判断拒绝原因。
var (
	ErrInvalidConfig      = &Error{Kind: KindInvalidConfig, msg: "hoppingwindow: invalid config"}
	ErrEmptyKey           = &Error{Kind: KindEmptyKey, msg: "hoppingwindow: empty key"}
	ErrClockRegression    = &Error{Kind: KindClockRegression, msg: "hoppingwindow: clock cannot go backwards"}
	ErrTooManyOpenWindows = &Error{Kind: KindTooManyOpenWindows, msg: "hoppingwindow: too many open windows"}
)

// Is 让 errors.Is(err, ErrXxx) 按 Kind 匹配，
// 这样携带具体说明文案的错误仍能被识别出原因。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// invalidConfigf 构造一个参数非法错误（Kind 仍为 KindInvalidConfig，
// 因此 errors.Is(err, ErrInvalidConfig) 成立，但文案可说明具体原因）。
func invalidConfigf(format string, args ...any) error {
	return &Error{Kind: KindInvalidConfig, msg: "hoppingwindow: invalid config: " + fmt.Sprintf(format, args...)}
}
