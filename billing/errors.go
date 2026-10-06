package billing

// ErrorCode 以稳定字符串标识每一类可区分的业务错误。
type ErrorCode string

const (
	// ErrInvalidArgument 参数非法，错误优先级最高。
	ErrInvalidArgument ErrorCode = "invalid_argument"
	// ErrAlreadySettled 周期已结算并封存。
	ErrAlreadySettled ErrorCode = "already_settled"
	// ErrVersionConflict 同版本号但内容不同。
	ErrVersionConflict ErrorCode = "version_conflict"
	// ErrStaleSample 版本号不大于该槽位已见过的最高版本号。
	ErrStaleSample ErrorCode = "stale_sample"
	// ErrVersionMismatch 撤回时版本号与槽位当前版本不一致。
	ErrVersionMismatch ErrorCode = "version_mismatch"
	// ErrNotFound 槽位当前没有采样。
	ErrNotFound ErrorCode = "not_found"
	// ErrNoSamples 当前没有任何有效槽位，计费速率无定义。
	ErrNoSamples ErrorCode = "no_samples"
	// ErrInsufficientData 缺失槽位比例超过容忍比例，不能结算。
	ErrInsufficientData ErrorCode = "insufficient_data"
)

// Error 携带固定错误码与可读说明，便于调用方按码分流。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Msg
}

func bizError(code ErrorCode, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}
