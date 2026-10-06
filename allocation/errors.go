package allocation

import "fmt"

// ErrorCode 错误类别，可区分；校验按 ErrorCode 数值从小到大的优先级只报第一个。
type ErrorCode int

const (
	ErrInvalidArgument   ErrorCode = iota // 参数非法
	ErrClockRollback                      // 时钟回退
	ErrNotFound                           // 教师或任务不存在
	ErrState                              // 状态不允许
	ErrConflict                           // 时段冲突
	ErrOverCap                            // 超出上限
	ErrHoursConservation                  // 学时分配不守恒
)

// Error 携带错误类别、批量下标（-1 表示非批量操作）与说明。
type Error struct {
	Code    ErrorCode
	Index   int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("allocation: code=%d index=%d: %s", e.Code, e.Index, e.Message)
}

func newErr(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Index: -1, Message: fmt.Sprintf(format, args...)}
}

func newErrAt(code ErrorCode, index int, format string, args ...any) *Error {
	return &Error{Code: code, Index: index, Message: fmt.Sprintf(format, args...)}
}
