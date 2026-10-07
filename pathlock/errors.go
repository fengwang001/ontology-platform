// Package pathlock 提供仓库级的大文件路径锁服务。
package pathlock

// ErrorCode 标识一次调用被拒绝的原因。
// 一次调用同时满足多个错误条件时，只报告按下表顺序最靠前的一个：
// ErrInvalidArgument > ErrPermissionDenied > ErrLockNotFound > ErrNotOwner >
// ErrHeldBySelf > ErrHeldByOther > ErrAncestorOrDescendantConflict。
type ErrorCode int

const (
	// ErrInvalidArgument 参数非法：空用户、非法路径、空批次等。
	ErrInvalidArgument ErrorCode = iota + 1
	// ErrPermissionDenied 无权强制释放：带强制标志但调用者不是管理员。
	ErrPermissionDenied
	// ErrLockNotFound 锁不存在：释放不存在的锁标识。
	ErrLockNotFound
	// ErrNotOwner 非持有者：释放他人持有的锁（未带强制标志）。
	ErrNotOwner
	// ErrHeldBySelf 已由本人持有：对本人已持有的路径再加锁。
	ErrHeldBySelf
	// ErrHeldByOther 已被他人持有：对他人已持有的路径再加锁。
	ErrHeldByOther
	// ErrAncestorOrDescendantConflict 祖先或后代已被他人持有。
	ErrAncestorOrDescendantConflict
)

// Error 是服务返回的唯一错误类型，*Error 实现 error 接口。
type Error struct {
	Code     ErrorCode
	Message  string
	Holder   string // ErrHeldByOther / ErrAncestorOrDescendantConflict 时的现持有者
	Conflict *Lock  // ErrAncestorOrDescendantConflict 时冲突的那把锁
}

func (e *Error) Error() string { return e.Message }

func invalidArg(msg string) *Error {
	return &Error{Code: ErrInvalidArgument, Message: "参数非法: " + msg}
}
