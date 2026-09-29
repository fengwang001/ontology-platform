package scd

import (
	"fmt"
	"strings"
)

// ErrorCode 用互不相同的错误码区分每一类整批拒绝原因。
type ErrorCode string

const (
	CodeEmptyBatch      ErrorCode = "empty_batch"
	CodeInvalidArgument ErrorCode = "invalid_argument"
	CodeEmptyKey        ErrorCode = "empty_key"
	CodeInvalidOp       ErrorCode = "invalid_op"
	CodeTimeOutOfRange  ErrorCode = "time_out_of_range"
	CodeTooManyChanges  ErrorCode = "too_many_change_points"
	CodeSelfCheck       ErrorCode = "self_check_failed"
)

// CommitError 聚合一次整批提交中出现的全部拒绝原因。
// 出现 CommitError 时变更点与历史必然未发生任何改变。
type CommitError struct {
	Reasons []RejectReason
}

// RejectReason 描述一条拒绝原因；同类原因的错误码相同、细节不同，可区分。
type RejectReason struct {
	Code    ErrorCode
	Index   int
	Key     string
	At      int64
	Message string
}

func (e *CommitError) Error() string {
	var sb strings.Builder
	sb.WriteString("commit rejected: ")
	for i, r := range e.Reasons {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(string(r.Code))
		sb.WriteString(": ")
		sb.WriteString(r.Message)
	}
	return sb.String()
}

// Codes 返回本次拒绝中出现的全部错误码，保持出现顺序、不去重。
func (e *CommitError) Codes() []ErrorCode {
	codes := make([]ErrorCode, len(e.Reasons))
	for i, r := range e.Reasons {
		codes[i] = r.Code
	}
	return codes
}

// HasCode 判断本次拒绝是否包含指定错误类别。
func (e *CommitError) HasCode(code ErrorCode) bool {
	for _, r := range e.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func (r RejectReason) String() string {
	return fmt.Sprintf("[%s] event#%d key=%q at=%d: %s", r.Code, r.Index, r.Key, r.At, r.Message)
}
