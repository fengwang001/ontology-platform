package lwwview

import (
	"errors"
	"fmt"
)

// 可区分的拒绝原因，调用方可用 errors.Is 精确判定。
var (
	// ErrInvalidArgument 参数非法：events 为 nil/空，或其中某条事件为 nil。
	ErrInvalidArgument = errors.New("lwwview: invalid argument")
	// ErrEmptyKey 事件键为空字符串。
	ErrEmptyKey = errors.New("lwwview: empty key")
	// ErrTooManyKeys 一批事件将使跟踪的不同键数量超过视图容量。
	ErrTooManyKeys = errors.New("lwwview: too many distinct keys")
)

// EventError 标出批次中触发拒绝原因的事件下标，原因可被 errors.Is 识别。
type EventError struct {
	Index int
	Err   error
}

func (e *EventError) Error() string {
	return fmt.Sprintf("lwwview: event[%d]: %v", e.Index, e.Err)
}

func (e *EventError) Unwrap() error { return e.Err }

// BatchError 表示整批事件被拒绝：状态不会发生任何变化。
// errors.Is(err, ErrEmptyKey) / errors.Is(err, ErrTooManyKeys) 仍可识别根因。
type BatchError struct {
	Reason string
	Inner  error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("lwwview: batch rejected (%s): %v", e.Reason, e.Inner)
}

func (e *BatchError) Unwrap() error { return e.Inner }
