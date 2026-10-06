package pathlock

// 错误码：错误次序即声明次序（参数非法 < 无权 < 锁不存在 < 非持有者
// < 已由本人持有 < 已被他人持有 < 祖先或后代冲突）。
const (
	ErrCodeInvalidPath      = iota // 参数非法：非法路径
	ErrCodeEmptyUser               // 参数非法：空用户
	ErrCodeEmptyBatch              // 参数非法：空批次
	ErrCodeNotAdmin                // 无权：非管理员强制释放
	ErrCodeLockNotFound            // 锁不存在
	ErrCodeNotOwner                // 非持有者
	ErrCodeSelfHeld                // 已由本人持有
	ErrCodeOtherHeld               // 已被他人持有
	ErrCodeAncestorConflict        // 祖先或后代已被他人持有
)

// LockError 携带错误码与裁决所依据的冲突锁（若有）。
type LockError struct {
	Code     int
	Conflict Lock
	hasLock  bool
}

func (e *LockError) Error() string {
	switch e.Code {
	case ErrCodeInvalidPath:
		return "invalid path"
	case ErrCodeEmptyUser:
		return "empty user"
	case ErrCodeEmptyBatch:
		return "empty batch"
	case ErrCodeNotAdmin:
		return "not authorized to force release"
	case ErrCodeLockNotFound:
		return "lock not found"
	case ErrCodeNotOwner:
		return "not the lock owner"
	case ErrCodeSelfHeld:
		return "already held by self"
	case ErrCodeOtherHeld:
		return "already held by another user"
	case ErrCodeAncestorConflict:
		return "ancestor or descendant held by another user"
	default:
		return "lock error"
	}
}

// 哨兵错误与构造函数在实现阶段补充。
var (
	ErrInvalidPath = &LockError{Code: ErrCodeInvalidPath}
	ErrEmptyUser   = &LockError{Code: ErrCodeEmptyUser}
	ErrEmptyBatch  = &LockError{Code: ErrCodeEmptyBatch}
)

func errNotAdmin() *LockError { return &LockError{Code: ErrCodeNotAdmin} }

func errLockNotFound() *LockError { return &LockError{Code: ErrCodeLockNotFound} }

func errNotOwner(l Lock) *LockError {
	return &LockError{Code: ErrCodeNotOwner, Conflict: l, hasLock: true}
}

func errSelfHeld(l Lock) *LockError {
	return &LockError{Code: ErrCodeSelfHeld, Conflict: l, hasLock: true}
}

func errOtherHeld(l Lock) *LockError {
	return &LockError{Code: ErrCodeOtherHeld, Conflict: l, hasLock: true}
}

func errAncestor(l Lock) *LockError {
	return &LockError{Code: ErrCodeAncestorConflict, Conflict: l, hasLock: true}
}

// ConflictLock 返回裁决所附的冲突锁与是否存在。
func (e *LockError) ConflictLock() (Lock, bool) { return e.Conflict, e.hasLock }
