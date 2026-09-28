package changelog

import "errors"

// 拒绝一批写入时返回的哨兵错误，调用方可用 errors.Is 区分原因。
var (
	// ErrEmptyKey 表示批中存在空键。
	ErrEmptyKey = errors.New("changelog: empty key")
	// ErrInvalidOp 表示批中存在非法操作类型（既非 OpPut 也非 OpDelete）。
	ErrInvalidOp = errors.New("changelog: invalid operation type")
	// ErrTooManyLiveKeys 表示批结束后存活键数超过构造时设定的上限。
	ErrTooManyLiveKeys = errors.New("changelog: live key count exceeds limit")
)
