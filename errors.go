package ontology

import "fmt"

// ErrCode 按题目要求的固定错误次序编号（仅报告第一个错误）。
type ErrCode int

const (
	ErrInvalidArgument ErrCode = iota // 参数非法
	ErrClockRollback                  // 时钟回退
	ErrLeaseNotFound                  // 租约不存在
	ErrIllegalState                   // 状态不允许该操作
	ErrRentAboveCap                   // 涨幅超过上限
	ErrReplyLate                      // 答复逾期
)

// CodeError 携带固定分类的操作错误。
type CodeError struct {
	Code ErrCode
	msg  string
}

func (e *CodeError) Error() string { return e.msg }

func errf(code ErrCode, format string, args ...any) error {
	return &CodeError{Code: code, msg: fmt.Sprintf(format, args...)}
}
