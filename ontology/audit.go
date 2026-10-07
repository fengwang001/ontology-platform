package ontology

import "time"

// EventKind 区分需要分别暴露的四类（及辅助）处理事件。
type EventKind int

const (
	// EventExcessMarked 基数下调触发链接被标记为超额。
	EventExcessMarked EventKind = iota
	// EventCreateRejected 新建请求因当前有效上限已满被拒绝。
	EventCreateRejected
	// EventPendingForceDeleted 待处理链接因所依赖对象已被撤销，
	// 无法转为保留而被强制转为删除（该判定优先于默认处理路径）。
	EventPendingForceDeleted
	// EventPendingRestored 待处理期间上限被上调导致的整体（或部分）恢复。
	EventPendingRestored
	// EventPendingKept 显式处理动作将待处理链接转为保留。
	EventPendingKept
	// EventPendingDeleted 显式/默认处理动作将待处理链接转为删除。
	EventPendingDeleted
	// EventLinkCreated 新建链接被接受（用于并发对照重放）。
	EventLinkCreated
	// EventLimitChanged 基数上限调整生效（用于并发对照重放）。
	EventLimitChanged
)

func (k EventKind) String() string {
	switch k {
	case EventExcessMarked:
		return "excess_marked"
	case EventCreateRejected:
		return "create_rejected"
	case EventPendingForceDeleted:
		return "pending_force_deleted"
	case EventPendingRestored:
		return "pending_restored"
	case EventPendingKept:
		return "pending_kept"
	case EventPendingDeleted:
		return "pending_deleted"
	case EventLinkCreated:
		return "link_created"
	case EventLimitChanged:
		return "limit_changed"
	}
	return "unknown"
}

// AuditEvent 是一次处理动作的完整记录：
// 触发原因（Trigger）、标记依据（Basis）与最终去向（Disposition），供事后核对。
type AuditEvent struct {
	Seq         uint64
	Kind        EventKind
	Time        time.Time
	LinkID      string
	TypeID      string
	Direction   Direction
	ObjectID    string
	Trigger     string // 触发原因
	Basis       string // 标记依据（确定性规则及排序键）
	Disposition string // 最终去向
	Limit       int    // 事件发生时的有效上限
	Registered  int    // 事件发生时的登记数量
}

// appliedOp 记录一个已串行生效的变更操作及其结果，
// 使并发执行的结果可以与朴素串行实现按同一全序重放对照。
type appliedOp struct {
	op       string // "create" / "setlimit"
	typeID   string
	linkID   string
	source   string
	target   string
	dir      Direction
	limit    int
	accepted bool
}
