package segmentlog

import (
	"errors"
	"fmt"
)

// 错误类别：每一类输入拒绝都有互不相同、可用 errors.Is 区分的根因。
var (
	// ErrInvalidConfig 配置非法（非正容量、总容量小于段容量、负保留窗口）。
	ErrInvalidConfig = errors.New("segmentlog: invalid config")
	// ErrInvalidRecord 记录非法（时间戳非正、数据为空、字节数超过单段容量）。
	ErrInvalidRecord = errors.New("segmentlog: invalid record")
	// ErrClockBackwards 时钟回退（记录时间戳早于已见时间戳，或保留判定时钟回退）。
	ErrClockBackwards = errors.New("segmentlog: clock moved backwards")
	// ErrOffsetOutOfRange 读取位点非法或落在已删除/空洞区间。
	ErrOffsetOutOfRange = errors.New("segmentlog: offset out of range")
	// ErrInvalidArgument 其它非法入参（如 Read 的负数位点）。
	ErrInvalidArgument = errors.New("segmentlog: invalid argument")
)

// Error 附带具体原因但仍可用 errors.Is 判定类别。
type categorizedError struct {
	cause error
	msg   string
}

func (e *categorizedError) Error() string { return e.msg }
func (e *categorizedError) Unwrap() error { return e.cause }

func reject(cause error, format string, args ...any) error {
	return &categorizedError{cause: cause, msg: fmt.Sprintf(format, args...)}
}
