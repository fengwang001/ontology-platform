package ontology

import "fmt"

// ErrorCode 按规则定义的错误类别。拒绝次序即下列常量的数值次序：
// 参数非法 > 不在挂接期内 > 与已有读数冲突 > 读数冲突 > 不合理 > 无读数。
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota + 1
	ErrNotAttached
	ErrReadingScheduleConflict
	ErrReadingConflict
	ErrUnreasonable
	ErrNoReading
)

// MeterError 携带可区分的错误类别，调用方可通过 errors.As 判定。
type MeterError struct {
	Code ErrorCode
	Msg  string
}

func (e *MeterError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidArgument:
		return "参数非法"
	case ErrNotAttached:
		return "不在挂接期内"
	case ErrReadingScheduleConflict:
		return "与已有读数冲突"
	case ErrReadingConflict:
		return "读数冲突"
	case ErrUnreasonable:
		return "不合理"
	case ErrNoReading:
		return "无读数"
	default:
		return "未知错误"
	}
}

func errf(code ErrorCode, format string, args ...any) error {
	return &MeterError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
