package pretty

import (
	"errors"
	"fmt"
)

// ErrCode 标识引擎返回的错误类别。
//
// 渲染时各类错误的报告优先级（高在前）：
//
//	ErrInvalidParam > ErrTooDeep > ErrUnregisteredRef > ErrOutputTooLarge
//
// 登记时各类错误的报告优先级（高在前）：
//
//	ErrInvalidParam > ErrDuplicateName > ErrUnregisteredRef
type ErrCode int

const (
	// ErrInvalidParam 表示参数非法：行宽小于 1 或大于 10000、缩进增量
	// 为负、文本含换行字符、片段名为空、文档为 nil。
	ErrInvalidParam ErrCode = iota + 1
	// ErrDuplicateName 表示登记的片段名与已有片段重复。
	ErrDuplicateName
	// ErrTooDeep 表示文档嵌套深度（含引用展开后）超过 1000。
	ErrTooDeep
	// ErrUnregisteredRef 表示引用了未登记的片段。
	ErrUnregisteredRef
	// ErrOutputTooLarge 表示渲染结果的总宽度超过 10^7。
	ErrOutputTooLarge
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrDuplicateName:
		return "duplicate fragment name"
	case ErrTooDeep:
		return "document too deep"
	case ErrUnregisteredRef:
		return "unregistered fragment reference"
	case ErrOutputTooLarge:
		return "output too large"
	default:
		return fmt.Sprintf("unknown error (%d)", int(c))
	}
}

// Error 是引擎返回的错误类型，携带类别与细节。
type Error struct {
	Code   ErrCode
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return "pretty: " + e.Code.String()
	}
	return fmt.Sprintf("pretty: %s: %s", e.Code, e.Detail)
}

// CodeOf 从 err 中提取错误类别；err 不是 *Error 时返回 false。
func CodeOf(err error) (ErrCode, bool) {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code, true
	}
	return 0, false
}
