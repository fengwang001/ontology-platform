package intervaljoin

import "fmt"

// ErrorCode 是一次被拒绝操作的原因码，可通过 errors.As 区分。
type ErrorCode string

const (
	// CodeInvalidParameter：参数非法（配置非法、事件为 nil、区间下界大于上界等）。
	CodeInvalidParameter ErrorCode = "invalid_parameter"
	// CodeEmptyKey：事件连接键为空。
	CodeEmptyKey ErrorCode = "empty_key"
	// CodeTimeRegression：单侧事件到达时间相对本侧水位线倒退。
	CodeTimeRegression ErrorCode = "time_regression"
	// CodeRetentionLimitExceeded：处理一步之后某侧保留事件数超过上限。
	CodeRetentionLimitExceeded ErrorCode = "retention_limit_exceeded"
)

// JoinError 描述一次被拒绝的操作及其原因。
// 被拒绝的操作不会改变水位线、编号、保留状态或已输出的配对。
type JoinError struct {
	// Code 为机器可读的原因码。
	Code ErrorCode
	// Side 为触发操作的流侧；与具体侧无关的参数错误为 -1。
	Side Side
	// Msg 为人类可读的细节。
	Msg string
}

func (e *JoinError) Error() string {
	return fmt.Sprintf("intervaljoin: %s on %s: %s", e.Code, e.Side, e.Msg)
}

func joinErrorf(code ErrorCode, side Side, format string, args ...any) *JoinError {
	return &JoinError{Code: code, Side: side, Msg: fmt.Sprintf(format, args...)}
}
