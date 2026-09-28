// Package groupagg 提供支持分组键变更的增量分组聚合组件。
//
// 组件随行的插入(Insert)、更新(Update)、删除(Delete)实时维护每个分组的
// 求和(sum)与计数(count)，并以“先撤回旧值、再写入新值”的有序变更日志
// (ChangeEntry) 输出净变化；下游按顺序应用该日志即可得到与批量重算完全
// 一致的分组聚合视图。
package groupagg

// OpKind 标识一行上的操作类型。
type OpKind int

const (
	// OpInsert 插入一行；行 ID 必须此前不存在。
	OpInsert OpKind = iota + 1
	// OpUpdate 更新一行的分组键与/或值；行必须此前存在。
	OpUpdate
	// OpDelete 删除一行；行必须此前存在，分组键与值字段被忽略。
	OpDelete
)

// Op 是作用在行表上的一次操作。
type Op struct {
	Kind     OpKind
	ID       string // 行唯一标识，不能为空
	GroupKey string // 目标分组键，Insert/Update 时不能为空
	Value    int64  // 行的值（求和口径），Delete 时忽略
}

// Row 是行表中一行的当前状态。
type Row struct {
	ID       string
	GroupKey string
	Value    int64
}

// GroupAgg 是一个分组当前的聚合值。计数恒大于 0（计数为 0 的组不在视图中）。
type GroupAgg struct {
	Sum   int64
	Count int64
}

// GroupView 是带键的分组聚合视图，用于按分组键排序后做确定性输出。
type GroupView struct {
	Group string
	Sum   int64
	Count int64
}

// ChangeKind 标识变更日志条目的语义。
type ChangeKind int

const (
	// ChangeRetract 撤回某分组此前的聚合值（下游应移除该分组旧值）。
	ChangeRetract ChangeKind = iota + 1
	// ChangePut 写入某分组当前的聚合值（下游以本条为准设置该分组）。
	ChangePut
)

// ChangeEntry 是变更日志中的一条记录。
//
// Retract 条目携带操作前该分组的完整 Sum/Count；Put 条目携带操作后该分组
// 的完整 Sum/Count。同一次操作受影响的分组，先输出旧分组再输出新分组，
// 每个分组内先 Retract 后 Put。
type ChangeEntry struct {
	Seq   int64      // 全局单调递增序号，从 1 开始
	RowID string     // 触发该条目的行 ID
	Kind  ChangeKind // Retract / Put
	Group string     // 分组键
	Sum   int64      // 该条目携带的分组求和
	Count int64      // 该条目携带的分组行数
}

// OpReport 描述一次操作的输入、输出条目与判定依据。
type OpReport struct {
	Op      Op
	Entries []ChangeEntry
	Basis   string // 判定依据的人类可读说明
}

// BatchResult 是一个被接受批次的结果。
type BatchResult struct {
	Reports []OpReport
	Entries []ChangeEntry // 本批次产生的全部条目，顺序即日志顺序
}
