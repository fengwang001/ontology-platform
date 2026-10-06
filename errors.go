package ontology

import "fmt"

// Code 是可区分的错误分类码。
type Code int

const (
	// ErrInvalidParam 参数非法（优先级最高）。
	ErrInvalidParam Code = iota + 1
	// ErrClockRegression 服务端时钟回退。
	ErrClockRegression
	// ErrSessionNotFound 会话不存在。
	ErrSessionNotFound
	// ErrSessionEnded 会话已结束或已结算。
	ErrSessionEnded
	// ErrCredential 凭证无效或代次过期（旧凭证/旧代次可由 SubCode 区分）。
	ErrCredential
	// ErrStateNotAllowed 当前会话状态不允许该操作。
	ErrStateNotAllowed
	// ErrAnswerLateOrDuplicate 作答迟到或重复（可由 SubCode 区分）。
	ErrAnswerLateOrDuplicate
)

const (
	// SubNone 无子类。
	SubNone = ""
	// SubStaleCredential 旧凭证（存在更近的一次暂停）。
	SubStaleCredential = "stale_credential"
	// SubUnknownCredential 凭证串本身无法识别。
	SubUnknownCredential = "unknown_credential"
	// SubOldGeneration 作答携带的会话代次已过期。
	SubOldGeneration = "old_generation"
	// SubLateAnswer 作答序号低于已落定水位（迟到）。
	SubLateAnswer = "late_answer"
	// SubDuplicateAnswer 作答序号与会话内某条已接受作答完全相同（重复提交）。
	SubDuplicateAnswer = "duplicate_answer"
	// SubSettled 会话已结算，重复提交被拒绝。
	SubSettled = "settled"
)

// Error 携带固定优先级的错误码与可区分子类。
type Error struct {
	Code    Code
	SubCode string
	Msg     string
}

func (e *Error) Error() string {
	if e.SubCode == SubNone {
		return fmt.Sprintf("exam: code=%d %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("exam: code=%d sub=%s %s", e.Code, e.SubCode, e.Msg)
}

func errf(code Code, sub, format string, args ...any) *Error {
	return &Error{Code: code, SubCode: sub, Msg: fmt.Sprintf(format, args...)}
}
