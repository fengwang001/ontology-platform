// Package changelog 把按批到达的写入流折叠为撤回式变更日志。
package changelog

import "fmt"

// Op 表示一条写入的操作类型。
type Op int

const (
	// OpPut 写入一个键值对（使键存在）。
	OpPut Op = iota + 1
	// OpDelete 删除一个键（使键不存在）；Value 字段被忽略。
	OpDelete
)

// Write 是批中的一条写入。
type Write struct {
	Op    Op
	Key   string
	Value string
}

// EntryKind 是变更日志条目的种类。
type EntryKind int

const (
	// EntryRetract 撤回一个此前存在的值。
	EntryRetract EntryKind = iota + 1
	// EntryInsert 写入一个新值。
	EntryInsert
)

// Entry 是一条撤回式变更日志条目。
type Entry struct {
	Kind  EntryKind
	Key   string
	Value string
}

// RejectReason 是批被拒绝的可区分原因。
type RejectReason int

const (
	// RejectEmptyKey 空键。
	RejectEmptyKey RejectReason = iota + 1
	// RejectInvalidOp 非法操作类型。
	RejectInvalidOp
	// RejectTooManyLiveKeys 批结束后存活键数超过上限。
	RejectTooManyLiveKeys
)

func (r RejectReason) String() string {
	switch r {
	case RejectEmptyKey:
		return "empty key"
	case RejectInvalidOp:
		return "invalid operation type"
	case RejectTooManyLiveKeys:
		return "too many live keys after batch"
	default:
		return "unknown reject reason"
	}
}

// RejectError 表示整批被拒绝；Reason 给出可区分的原因。
type RejectError struct {
	Reason RejectReason
	// Index 是触发拒绝的写入在批中的下标（RejectTooManyLiveKeys 时为 -1）。
	Index int
	// Detail 是人可读的补充信息。
	Detail string
}

func (e *RejectError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("changelog: batch rejected (%s) at index %d: %s", e.Reason, e.Index, e.Detail)
	}
	return fmt.Sprintf("changelog: batch rejected (%s) at index %d", e.Reason, e.Index)
}
