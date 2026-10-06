package reflow

import "fmt"

// ErrorKind 将错误划分为四类，拒绝次序为
// 参数非法 < 节点不存在 < 结构冲突 < 提交中重入。
type ErrorKind uint8

const (
	KindInvalidArg      ErrorKind = 1
	KindNodeNotFound    ErrorKind = 2
	KindConflict        ErrorKind = 3
	KindReentrantCommit ErrorKind = 4
)

// KernelError 是内核返回的唯一错误类型，可按 Kind 区分。
type KernelError struct {
	Kind ErrorKind
	Op   string
	Msg  string
}

func (e *KernelError) Error() string {
	return fmt.Sprintf("reflow: %s: %s", e.Op, e.Msg)
}

func kerr(kind ErrorKind, op, format string, args ...any) error {
	return &KernelError{Kind: kind, Op: op, Msg: fmt.Sprintf(format, args...)}
}

// KindOf 返回错误所属类别；非内核错误返回 0。
func KindOf(err error) ErrorKind {
	if e, ok := err.(*KernelError); ok {
		return e.Kind
	}
	return 0
}
