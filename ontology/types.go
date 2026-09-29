package ontology

// OpKind 标识变更流中的四类操作。
type OpKind string

const (
	// OpInsertParent 插入（或重放）父行，幂等。
	OpInsertParent OpKind = "insert_parent"
	// OpDeleteParent 删除父行；父行不存在或仍被子行引用时拒绝。
	OpDeleteParent OpKind = "delete_parent"
	// OpInsertChild 插入子行；父行当前必须存在，否则拒绝且不留记忆。
	OpInsertChild OpKind = "insert_child"
	// OpDeleteChild 删除子行；子行不存在时拒绝。
	OpDeleteChild OpKind = "delete_child"
)

// ErrKind 给出可区分的失败原因。
type ErrKind string

const (
	// ErrChildParentMissing 子行插入时父行不存在。
	ErrChildParentMissing ErrKind = "child_parent_missing"
	// ErrParentReferenced 删除父行时父行仍被子行引用。
	ErrParentReferenced ErrKind = "parent_referenced"
	// ErrParentNotFound 删除的父行不存在。
	ErrParentNotFound ErrKind = "parent_not_found"
	// ErrChildNotFound 删除的子行不存在。
	ErrChildNotFound ErrKind = "child_not_found"
	// ErrReplayMismatch 重放成功操作日志得到的视图与期望视图不一致。
	ErrReplayMismatch ErrKind = "replay_mismatch"
)

// Op 是变更流上的一条操作。
type Op struct {
	Kind OpKind
	// ParentID 是父行主键，四类操作均需提供。
	ParentID string
	// ChildID 是子行主键，仅子行操作需要。
	ChildID string
}

// ParentRow 是父表中的一行。
type ParentRow struct {
	ID string
}

// ChildRow 是子表中的一行，ParentID 必须引用一个当前存在的父行。
type ChildRow struct {
	ID       string
	ParentID string
}

// View 是某一时刻父表与子表的不可变快照（均已按主键排序）。
type View struct {
	Parents  []ParentRow
	Children []ChildRow
}

// ReplayResult 是用一条只含成功操作的日志做重放核对的结果。
type ReplayResult struct {
	OK        bool
	View      View
	ErrorKind ErrKind
}
