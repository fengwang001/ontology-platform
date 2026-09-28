// Package cdc 提供主键变更的拆分与分区投递组件：
// 将源表变更拆分为下游可识别的写/删事件，并按主键哈希分区投递，
// 保证下游视图与源表始终一致。
package cdc

import "fmt"

// Row 表示源表中的一行，Key 为主键。
type Row struct {
	Key    string
	Fields map[string]string
}

// ChangeType 变更类型。
type ChangeType int

const (
	ChangeInsert ChangeType = iota
	ChangeUpdate
	ChangeDelete
)

func (t ChangeType) String() string {
	switch t {
	case ChangeInsert:
		return "INSERT"
	case ChangeUpdate:
		return "UPDATE"
	case ChangeDelete:
		return "DELETE"
	}
	return "UNKNOWN"
}

// Change 描述一条源表变更。
// Insert 使用 After；Delete 使用 Before；Update 同时使用 Before 与 After。
type Change struct {
	Type   ChangeType
	Before Row
	After  Row
}

// EventType 下游事件类型。
type EventType int

const (
	EventWrite EventType = iota
	EventDelete
)

func (t EventType) String() string {
	if t == EventWrite {
		return "WRITE"
	}
	return "DELETE"
}

// Event 是投递给下游的事件：写入或删除某个主键。
type Event struct {
	Seq  uint64 // 全局单调递增序号
	Type EventType
	Key  string
	Row  Row // 仅 WRITE 事件有效
}

func (e Event) String() string {
	if e.Type == EventWrite {
		return fmt.Sprintf("#%d WRITE %s=%v", e.Seq, e.Key, e.Row.Fields)
	}
	return fmt.Sprintf("#%d DELETE %s", e.Seq, e.Key)
}

// RejectReason 批被拒绝的可区分原因。
type RejectReason string

const (
	ReasonInvalidKey  RejectReason = "INVALID_KEY"   // 主键非法（空）
	ReasonKeyExists   RejectReason = "KEY_EXISTS"    // 插入的键已存在
	ReasonKeyNotFound RejectReason = "KEY_NOT_FOUND" // 更新/删除的键不存在
	ReasonTooManyRows RejectReason = "TOO_MANY_ROWS" // 批行数超限
)

// BatchRejectedError 表示整个批被拒绝，批内任何变更都不会生效。
type BatchRejectedError struct {
	Index  int          // 触发拒绝的变更在批内的下标，行数超限时为 -1
	Reason RejectReason // 可区分的拒绝原因
	Detail string       // 判定依据
}

func (e *BatchRejectedError) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("batch rejected at change[%d]: %s: %s", e.Index, e.Reason, e.Detail)
	}
	return fmt.Sprintf("batch rejected: %s: %s", e.Reason, e.Detail)
}
