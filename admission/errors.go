package admission

import "fmt"

// ErrorClass 是可区分的错误类别，数值越大优先级越高。
type ErrorClass int

const (
	ClassTimeout          ErrorClass = 1 // 排队超时（错误类别中优先级最低）
	ClassQueueFull        ErrorClass = 2 // 队列已满
	ClassInsufficientSeat ErrorClass = 3 // 席位不可满足
	ClassNoMatch          ErrorClass = 4 // 无匹配规则
	ClassClockRewind      ErrorClass = 5 // 时钟回退
	ClassInvalidArgument  ErrorClass = 6 // 参数非法（优先级最高）
)

func (c ErrorClass) String() string {
	switch c {
	case ClassTimeout:
		return "queue-timeout"
	case ClassQueueFull:
		return "queue-full"
	case ClassInsufficientSeat:
		return "seat-insatisfiable"
	case ClassNoMatch:
		return "no-match"
	case ClassClockRewind:
		return "clock-rewind"
	case ClassInvalidArgument:
		return "invalid-argument"
	default:
		return fmt.Sprintf("unknown-error-class(%d)", int(c))
	}
}

// AdmissionError 携带错误类别与可读原因；多重错误只暴露优先级最高者。
type AdmissionError struct {
	Class ErrorClass
	msg   string
}

func (e *AdmissionError) Error() string {
	return e.Class.String() + ": " + e.msg
}

func newError(class ErrorClass, msg string) *AdmissionError {
	return &AdmissionError{Class: class, msg: msg}
}
