// Package pkchange 提供主键变更的拆分与分区投递组件：
// 将源表变更（插入/更新/删除）拆分为下游可识别的写入/删除事件，
// 按主键哈希分区投递，保证下游视图与源表始终一致。
package pkchange

import "fmt"

// ChangeType 源表变更类型。
type ChangeType int

const (
	// Insert 插入一行。
	Insert ChangeType = iota
	// Update 更新一行；NewKey 非空且不同于 Key 时表示主键变更。
	Update
	// Delete 删除一行。
	Delete
)

func (t ChangeType) String() string {
	switch t {
	case Insert:
		return "INSERT"
	case Update:
		return "UPDATE"
	case Delete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// Change 一条源表变更。
type Change struct {
	Type   ChangeType
	Key    string // 主键（Update 时为旧主键）
	NewKey string // 仅 Update 且主键变更时使用，为新主键
	Value  string // 行内容（Delete 时忽略）
}

// EventType 下游事件类型。
type EventType int

const (
	// EventPut 写入/覆盖一个键。
	EventPut EventType = iota
	// EventDelete 删除一个键。
	EventDelete
)

func (t EventType) String() string {
	switch t {
	case EventPut:
		return "PUT"
	case EventDelete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// Event 一条下游事件，Seq 为全局单调递增序号。
type Event struct {
	Seq   int
	Type  EventType
	Key   string
	Value string
}

func (e Event) String() string {
	if e.Type == EventPut {
		return fmt.Sprintf("#%d PUT %s=%q", e.Seq, e.Key, e.Value)
	}
	return fmt.Sprintf("#%d DELETE %s", e.Seq, e.Key)
}

// RejectReason 批被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonInvalidKey 主键非法（空键，或主键变更的新键为空）。
	ReasonInvalidKey RejectReason = "INVALID_KEY"
	// ReasonKeyExists 插入的键已存在。
	ReasonKeyExists RejectReason = "KEY_EXISTS"
	// ReasonKeyNotFound 更新/删除的键不存在。
	ReasonKeyNotFound RejectReason = "KEY_NOT_FOUND"
	// ReasonTooManyRows 批行数超过上限。
	ReasonTooManyRows RejectReason = "TOO_MANY_ROWS"
)

// RejectError 批校验失败错误，携带可区分的原因与定位信息。
type RejectError struct {
	Reason RejectReason
	Index  int    // 触发拒绝的变更在批中的下标，行数超限时为 -1
	Key    string // 相关主键
	Detail string
}

func (e *RejectError) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("batch rejected (%s) at change[%d] key=%q: %s", e.Reason, e.Index, e.Key, e.Detail)
	}
	return fmt.Sprintf("batch rejected (%s): %s", e.Reason, e.Detail)
}
