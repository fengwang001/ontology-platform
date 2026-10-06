package toollife

// Code 是可区分的错误类别，取值顺序即错误判定优先级。
type Code int

const (
	// ErrInvalid 参数非法（最高优先级）。
	ErrInvalid Code = iota + 1
	// ErrGroupNotFound 刀组不存在。
	ErrGroupNotFound
	// ErrToolNotFound 刀具不存在。
	ErrToolNotFound
	// ErrConflict 同一申请编号提交了不同内容。
	ErrConflict
	// ErrState 当前状态不允许该操作。
	ErrState
	// ErrRequestNotFound 记账/中止时申请编号不存在或已终结。
	ErrRequestNotFound
	// ErrNoTool 无刀可用（含已耗尽、破损、锁定等）。
	ErrNoTool
	// ErrNoCapacity 暂无余量：所有刀都被预占占满，释放后可能成功。
	ErrNoCapacity
)

// Error 携带错误码与可读信息。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(code Code, format string, args ...any) error {
	return &Error{Code: code, Msg: sprintf(format, args...)}
}
