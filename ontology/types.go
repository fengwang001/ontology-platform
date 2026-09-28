// Package ontology 提供支持分组键变更的增量分组聚合组件。
package ontology

import "errors"

// OpKind 标识行级操作类型。
type OpKind int

const (
	// OpInsert 插入一行新数据。
	OpInsert OpKind = iota + 1
	// OpUpdate 更新已有行（可同时变更分组键与值）。
	OpUpdate
	// OpDelete 删除已有行。
	OpDelete
)

// EntryKind 标识一条变更日志条目是撤回旧值还是写入新值。
type EntryKind int

const (
	// EntryRetract 撤回（从组中减去旧值、计数减一）。
	EntryRetract EntryKind = iota + 1
	// EntryAdd 写入（向组中加入新值、计数加一）。
	EntryAdd
)

// Row 是行表中的一行：所属分组键与参与求和的值。
type Row struct {
	Group string
	Value int64
}

// Op 是一次行级变更操作。
//
// Insert 需要 RowID/Group/Value；Update 需要 RowID/Group/Value（Group 即新分组键，
// 与旧值不同即发生分组键变更）；Delete 只需要 RowID。
type Op struct {
	Kind  OpKind
	RowID string
	Group string
	Value int64
}

// GroupView 是某个分组对外可见的聚合快照。计数为零的组不会出现在视图中。
type GroupView struct {
	Group string
	Sum   int64
	Count int64
}

// LogEntry 是一条按序输出的净变化日志条目。
//
// 下游按 Seq 顺序应用 Value/Count 增量即可还原与批量重算一致的分组聚合视图；
// SumAfter/CountAfter 为应用该条目后该组的聚合结果（计数归零表示组已消失）。
type LogEntry struct {
	Seq        int       // 全局单调序号
	OpIndex    int       // 条目在所属批次中的操作下标
	RowID      string    // 触发行
	Group      string    // 受影响分组
	Kind       EntryKind // 撤回 / 写入
	Value      int64     // 应用到组求和上的带符号增量
	Count      int64     // 应用到组计数上的带符号增量（-1 或 +1）
	SumAfter   int64     // 应用后组求和
	CountAfter int64     // 应用后组计数
}

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrDuplicateRow 插入了行表中已存在（或同一批次中已出现）的行 ID。
	ErrDuplicateRow = errors.New("duplicate row: row id already exists")
	// ErrRowNotFound 更新或删除了行表中不存在的行。
	ErrRowNotFound = errors.New("row not found")
	// ErrEmptyRowKey 行 ID 为空。
	ErrEmptyRowKey = errors.New("invalid op: empty row id")
	// ErrEmptyGroupKey 插入或更新使用了空分组键。
	ErrEmptyGroupKey = errors.New("invalid op: empty group key")
	// ErrTooManyGroups 操作后非空组数超过聚合器上限。
	ErrTooManyGroups = errors.New("group limit exceeded: too many distinct groups")
	// ErrInvalidOp 未知操作类型。
	ErrInvalidOp = errors.New("invalid op: unknown operation kind")
)

// RejectError 在标准错误之外补充被拒绝操作的批次下标，便于定位。
type RejectError struct {
	Index int   // 批次中第一条非法操作的下标
	Op    Op    // 被拒绝的操作
	Cause error // 上述哨兵错误之一
}

func (e *RejectError) Error() string {
	return e.Cause.Error()
}

func (e *RejectError) Unwrap() error { return e.Cause }
