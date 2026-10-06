package idb

import "fmt"

// Kind 区分八类必备错误（另加 Aborted 用于版本变更中止）。
// 拒绝次序（先检查者优先）：
// 参数非法 > 版本过低 > 状态不允许 > 仓库不存在 > 作用域不符 > 事务已结束 > 键冲突。
// 被拒绝（rejected）的操作不改变版本、仓库、事务状态与队列；
// 只有运行期失败（如键冲突）才会按规则中止事务，除非请求标记 IgnoreError。
type Kind int

const (
	KindNone Kind = iota
	KindInvalidArg
	KindVersionTooLow
	KindInvalidState
	KindStoreNotFound
	KindScopeMismatch
	KindTxFinished
	KindKeyConflict
	KindCancelled
	KindAborted
)

var kindNames = map[Kind]string{
	KindNone:          "none",
	KindInvalidArg:    "invalid-argument",
	KindVersionTooLow: "version-too-low",
	KindInvalidState:  "invalid-state",
	KindStoreNotFound: "store-not-found",
	KindScopeMismatch: "scope-mismatch",
	KindTxFinished:    "transaction-finished",
	KindKeyConflict:   "key-conflict",
	KindCancelled:     "cancelled",
	KindAborted:       "aborted",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown"
}

// Error 是内核返回的唯一错误类型，Kind 字段承载类别。
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("idb: %s: %s", e.Kind, e.Msg) }

// KindOf 提取错误类别；nil 或非内核错误返回 KindNone。
func KindOf(err error) Kind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return KindNone
}

func errf(k Kind, format string, args ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}
