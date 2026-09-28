// Package clock 实现分布式逻辑时钟（Lamport clock）与事件全序、因果判定组件。
package clock

import "fmt"

// ErrorKind 是可区分的拒绝原因种类。
type ErrorKind string

const (
	// KindNodeOutOfRange 节点编号越界。
	KindNodeOutOfRange ErrorKind = "node_out_of_range"
	// KindMessageNotFound 接收了不存在（或对该节点不可见）的消息。
	KindMessageNotFound ErrorKind = "message_not_found"
	// KindDuplicateReceive 同一条消息被重复接收。
	KindDuplicateReceive ErrorKind = "duplicate_receive"
	// KindEventLimitExceeded 事件总数超过系统上限。
	KindEventLimitExceeded ErrorKind = "event_limit_exceeded"
)

// Error 携带可区分的拒绝原因及现场细节，便于调用方按 Kind 分支处理，
// 也便于日志打印判定依据。
type Error struct {
	Kind      ErrorKind
	Op        string // 触发拒绝的操作，如 "local" / "send" / "receive"
	Node      int    // 相关节点（若适用）
	MessageID int64  // 相关消息（若适用）
	Detail    string // 人类可读补充说明
}

func (e *Error) Error() string {
	return fmt.Sprintf("clock: %s: %s node=%d message=%d %s",
		e.Op, e.Kind, e.Node, e.MessageID, e.Detail)
}

// Is 使 errors.Is 按 Kind 比较哨兵错误，而不是按指针身份。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Kind == t.Kind
}

// 哨兵错误，调用方可用 errors.Is(err, clock.ErrXxx) 区分拒绝原因。
var (
	ErrNodeOutOfRange   = &Error{Kind: KindNodeOutOfRange}
	ErrMessageNotFound  = &Error{Kind: KindMessageNotFound}
	ErrDuplicateReceive = &Error{Kind: KindDuplicateReceive}
	ErrEventLimit       = &Error{Kind: KindEventLimitExceeded}
)
