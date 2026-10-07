package hypcheck

import "fmt"

// ErrorClass 是需求规定的四类互不相同的历史性输入错误。
// 同一次预检同时满足多类条件时，严格按本枚举的数字升序（E1 > E2 > E3 > E4）
// 只报告其中一类，该优先级在所有请求下保持一致。
type ErrorClass string

const (
	// E1: 指定历史时刻早于动作类型被定义的时刻（含类型定义被删除情形）。
	ErrTypeUndefined ErrorClass = "E1_TYPE_UNDEFINED"
	// E2: 校验钩子版本或权限继承快照因历史数据缺失（日志截断 / 缺口）无法重建。
	ErrHistoryGap ErrorClass = "E2_HISTORY_GAP"
	// E3: 调用者身份在指定历史时刻尚不存在（或当时已注销）。
	ErrCallerUnknown ErrorClass = "E3_CALLER_UNKNOWN"
	// E4: 动作参数违反当时生效的结构约束。
	ErrBadParams ErrorClass = "E4_BAD_PARAMS"
)

// PrecheckError 是带分类的预检错误；任何该错误产生路径均不得写入状态。
type PrecheckError struct {
	Class   ErrorClass
	Code    string
	Message string
}

func (e *PrecheckError) Error() string {
	return fmt.Sprintf("%s[%s]: %s", e.Class, e.Code, e.Message)
}

func newError(class ErrorClass, code, format string, args ...any) *PrecheckError {
	return &PrecheckError{Class: class, Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrorPriority 给出错误类的优先级（数字越小优先级越高）。
func ErrorPriority(c ErrorClass) int {
	switch c {
	case ErrTypeUndefined:
		return 1
	case ErrHistoryGap:
		return 2
	case ErrCallerUnknown:
		return 3
	case ErrBadParams:
		return 4
	default:
		return 100
	}
}
