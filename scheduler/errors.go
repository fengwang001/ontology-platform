package scheduler

import "fmt"

// 拒绝原因码。每一种非法输入都对应唯一、稳定、可区分的原因码，
// 调用方可用 errors.As / RejectError.Code 精确判别。
const (
	CodeInvalidArgument     = "INVALID_ARGUMENT"      // 参数非法（空批次、非法并行度、越界序号等）
	CodeSequenceGap         = "SEQUENCE_GAP"          // 事务序号不连续或未按 1 递增
	CodeEmptyWriteSet       = "EMPTY_WRITE_SET"       // 事务写集为空
	CodeEmptyWriteKey       = "EMPTY_WRITE_KEY"       // 事务写集包含空键
	CodeTooManyTransactions = "TOO_MANY_TRANSACTIONS" // 已接受事务总数超过上限
)

// RejectError 描述一次被整体拒绝的接收请求。
// 任何拒绝都不会改动调度器已有状态。
type RejectError struct {
	Code   string // 机器可读原因码，见 Code* 常量
	Reason string // 人类可读的详细原因
	Detail string // 可选上下文（如期望序号与实际序号）
}

func (e *RejectError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("scheduler: reject [%s] %s (%s)", e.Code, e.Reason, e.Detail)
	}
	return fmt.Sprintf("scheduler: reject [%s] %s", e.Code, e.Reason)
}

func reject(code, reason, detail string) *RejectError {
	return &RejectError{Code: code, Reason: reason, Detail: detail}
}
