package teaching

import "fmt"

// ErrorCode 为所有可区分的错误类别。按规则只报优先级最高的一个。
type ErrorCode int

const (
	ErrInvalidParameter  ErrorCode = iota + 1 // 参数非法
	ErrClockRollback                          // 时钟回退
	ErrNotFound                               // 教师或任务不存在
	ErrIllegalState                           // 状态不允许
	ErrSlotConflict                           // 时段冲突
	ErrOverCap                                // 超出上限
	ErrHoursNotConserved                      // 学时分配不守恒
)

// Error 携带错误类别及可读说明，同时记录批量失败时下标最小的失败项。
type Error struct {
	Code  ErrorCode
	Index int // 批量指派中失败项下标；非批量操作为 -1
	Msg   string
}

func (e *Error) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("teaching: code=%d index=%d: %s", e.Code, e.Index, e.Msg)
	}
	return fmt.Sprintf("teaching: code=%d: %s", e.Code, e.Msg)
}

func errf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Index: -1, Msg: fmt.Sprintf(format, args...)}
}

// priority 越小优先级越高，错误上报严格按此顺序取第一个。
var priority = map[ErrorCode]int{
	ErrInvalidParameter:  1,
	ErrClockRollback:     2,
	ErrNotFound:          3,
	ErrIllegalState:      4,
	ErrSlotConflict:      5,
	ErrOverCap:           6,
	ErrHoursNotConserved: 7,
}

// higherPriority 判断 a 是否比 b 优先级更高。
func higherPriority(a, b ErrorCode) bool { return priority[a] < priority[b] }
