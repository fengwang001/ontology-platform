package billing

import "fmt"

// Code 是稳定的错误分类码，调用方据此精确区分各类拒绝原因。
type Code int

const (
	CodeInvalidArgument  Code = iota + 1 // 参数非法
	CodeSettled                          // 周期已结算
	CodeVersionConflict                  // 版本冲突（版本相等内容不同）
	CodeStaleSample                      // 过期采样（版本号不大于已见最高版本）
	CodeVersionMismatch                  // 版本不符（撤回时版本号不等于当前版本）
	CodeNotFound                         // 不存在（槽位当前无采样）
	CodeNoSamples                        // 无采样（K 为 0，计费速率无定义）
	CodeInsufficientData                 // 数据不足（缺失比例超过容忍比例）
)

// Error 携带固定错误码，调用方按固定优先级处理。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("billing: %s: %s", e.Code, e.Msg)
}

func (c Code) String() string {
	switch c {
	case CodeInvalidArgument:
		return "参数非法"
	case CodeSettled:
		return "已结算"
	case CodeVersionConflict:
		return "版本冲突"
	case CodeStaleSample:
		return "过期采样"
	case CodeVersionMismatch:
		return "版本不符"
	case CodeNotFound:
		return "不存在"
	case CodeNoSamples:
		return "无采样"
	case CodeInsufficientData:
		return "数据不足"
	default:
		return "未知错误"
	}
}

func errInvalid(format string, args ...any) error {
	return &Error{Code: CodeInvalidArgument, Msg: fmt.Sprintf(format, args...)}
}
func errSettled(format string, args ...any) error {
	return &Error{Code: CodeSettled, Msg: fmt.Sprintf(format, args...)}
}
func errConflict(format string, args ...any) error {
	return &Error{Code: CodeVersionConflict, Msg: fmt.Sprintf(format, args...)}
}
func errStale(format string, args ...any) error {
	return &Error{Code: CodeStaleSample, Msg: fmt.Sprintf(format, args...)}
}
func errMismatch(format string, args ...any) error {
	return &Error{Code: CodeVersionMismatch, Msg: fmt.Sprintf(format, args...)}
}
func errNotFound(format string, args ...any) error {
	return &Error{Code: CodeNotFound, Msg: fmt.Sprintf(format, args...)}
}
func errNoSamples(format string, args ...any) error {
	return &Error{Code: CodeNoSamples, Msg: fmt.Sprintf(format, args...)}
}
func errInsufficient(format string, args ...any) error {
	return &Error{Code: CodeInsufficientData, Msg: fmt.Sprintf(format, args...)}
}
