package lifecycle

// EventKind 审计事件类别。
type EventKind string

const (
	EventBirth  EventKind = "birth"
	EventWrite  EventKind = "write"
	EventRead   EventKind = "read"
	EventDelete EventKind = "delete"
	EventRevive EventKind = "revive"
)

// Event 是归属到某一段存活区间的一条审计记录。
type Event struct {
	Seq        int64 // 对象内单调递增
	Kind       EventKind
	At         int64 // 全局逻辑时钟时点
	IntervalID string
	Actor      string
	Detail     string

	// 删除 / 复活事件记录的权限快照引用。
	Permission *PermissionSnapshot

	// 复活事件专有字段。
	TargetDeleteAt int64 // 精确指向要延续的那一次删除事件
	RestoreLinks   bool  // 是否声明尝试恢复删除前持有的链接关系
	RestoredLinks  []string
}

// AuditReport 按对象返回的完整溯源视图。
type AuditReport struct {
	ObjectID  string
	Intervals []*Interval
	Events    []*Event
}
