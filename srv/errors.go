package srv

import "fmt"

// ErrCode 区分操作被拒绝的具体原因。
type ErrCode int

const (
	// ErrInvalidParam 参数非法（target、port、priority、weight、ttl、cooldown 越界）。
	ErrInvalidParam ErrCode = iota
	// ErrInvalidTime 时间非法（now 小于 0 或大于 1e15）。
	ErrInvalidTime
	// ErrFull 容量已满（淘汰到期记录后仍达 Cap 且标识为新）。
	ErrFull
	// ErrNotFound 记录不存在。
	ErrNotFound
	// ErrExpired 记录已到期。
	ErrExpired
	// ErrNoAvailable 没有任何可用记录。
	ErrNoAvailable
)

// Error 是 Selector 返回的错误类型，Code 可区分拒绝原因。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
