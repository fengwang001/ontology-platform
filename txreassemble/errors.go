// 事件被拒绝时的可区分原因与错误类型定义。
package txreassemble

import "fmt"

// RejectKind 是操作被拒绝的可区分原因。
type RejectKind int

const (
	// RejectUnknown 占位零值，不表示任何真实原因。
	RejectUnknown RejectKind = iota
	// RejectInvalidEvent 事件本身非法：未知类型、缺少事务 ID、写事件缺少行等。
	RejectInvalidEvent
	// RejectDuplicateBegin 对已在进行中的事务重复 Begin。
	RejectDuplicateBegin
	// RejectTxNotActive 对不在进行中的事务 Write / Commit / Rollback。
	RejectTxNotActive
	// RejectBufferFull 写入会使进行中事务的总缓冲行数超过上限。
	RejectBufferFull
)

// String 返回拒绝原因的可读名称，便于日志区分判定依据。
func (k RejectKind) String() string {
	switch k {
	case RejectInvalidEvent:
		return "invalid_event"
	case RejectDuplicateBegin:
		return "duplicate_begin"
	case RejectTxNotActive:
		return "tx_not_active"
	case RejectBufferFull:
		return "buffer_full"
	default:
		return "unknown"
	}
}

// RejectError 描述一次被拒绝的操作及其可区分原因。
type RejectError struct {
	Kind RejectKind
	TxID string
	msg  string
}

// Error 实现 error。
func (e *RejectError) Error() string {
	return fmt.Sprintf("txreassemble: rejected(%s) tx=%q: %s", e.Kind, e.TxID, e.msg)
}

func newReject(kind RejectKind, txID, msg string) *RejectError {
	return &RejectError{Kind: kind, TxID: txID, msg: msg}
}
