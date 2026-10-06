package reins

import "errors"

// Code 标识可被调用方区分的错误类别。
type Code int

const (
	CodeNone Code = iota
	CodeInvalidParam
	CodePolicyNotFound
	CodePolicyDuplicate
	CodeCapacityExceeded
	CodeClaimExists
	CodeClaimNotFound
	CodeAccidentNotCovered
)

// Error 是引擎返回的唯一错误类型，Code 用于分类判断。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// CodeOf 提取错误类别；非引擎错误返回 CodeNone。
func CodeOf(err error) Code {
	var re *Error
	if errors.As(err, &re) {
		return re.Code
	}
	return CodeNone
}
