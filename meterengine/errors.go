package meterengine

import "fmt"

// ErrorClass 为引擎所有可拒绝错误的稳定分类。
type ErrorClass int

const (
	ClassInvalidArgument ErrorClass = iota + 1
	ClassNotAttached
	ClassConflict
	ClassReadingConflict
	ClassUnreasonable
	ClassNoReading
)

// EngineError 携带固定优先级的错误类别与可读信息。
type EngineError struct {
	Class ErrorClass
	Msg   string
}

func (e *EngineError) Error() string { return e.Msg }

func errInvalid(format string, a ...any) error {
	return &EngineError{ClassInvalidArgument, "参数非法: " + fmt.Sprintf(format, a...)}
}

func errNotAttached(format string, a ...any) error {
	return &EngineError{ClassNotAttached, "不在挂接期内: " + fmt.Sprintf(format, a...)}
}

func errConflict(format string, a ...any) error {
	return &EngineError{ClassConflict, "与已有读数冲突: " + fmt.Sprintf(format, a...)}
}

func errReadingConflict(format string, a ...any) error {
	return &EngineError{ClassReadingConflict, "读数冲突: " + fmt.Sprintf(format, a...)}
}

func errUnreasonable(format string, a ...any) error {
	return &EngineError{ClassUnreasonable, "不合理: " + fmt.Sprintf(format, a...)}
}

func errNoReading(format string, a ...any) error {
	return &EngineError{ClassNoReading, "无读数: " + fmt.Sprintf(format, a...)}
}
