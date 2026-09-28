// Package replication 实现带前像（before-image）校验的变更复制应用组件。
//
// 变更事件逐条应用到内存副本表：仅当事件的前像与当前行完全一致时才应用，
// 否则该事件被分类为冲突并跳过。整批要么完整生效、要么完全不生效。
package replication

// Op 是变更操作类型。
type Op string

const (
	// OpInsert 插入：前像必须为空（行不存在），后像必须非空。
	OpInsert Op = "INSERT"
	// OpUpdate 更新：前像与后像都必须非空。
	OpUpdate Op = "UPDATE"
	// OpDelete 删除：前像必须非空，后像必须为空（行被删除）。
	OpDelete Op = "DELETE"
)

// Row 是一行的列镜像：列名 -> 值的集合。
// 列是否存在（key 是否在 map 中）本身就是镜像的一部分：
// 缺列与空串 "" 不相等。
type Row map[string]string

// Event 是一条携带前后像的变更事件。
type Event struct {
	// Seq 事件序号，批内必须从已处理序号 +1 开始严格连续。
	Seq int64
	// Op 操作类型。
	Op Op
	// Key 目标行主键。
	Key string
	// Before 前像：插入时必须为 nil；更新/删除时必须非 nil。
	Before Row
	// After 后像：插入/更新时必须非 nil；删除时必须为 nil。
	After Row
}

// ConflictKind 是冲突的分类（冲突不是错误）。
type ConflictKind string

const (
	// ConflictRowExists 插入时行已存在（前像为"不存在"，当前行存在）。
	ConflictRowExists ConflictKind = "ROW_EXISTS"
	// ConflictRowMissing 更新/删除时行不存在。
	ConflictRowMissing ConflictKind = "ROW_MISSING"
	// ConflictBeforeMismatch 行存在，但前像与当前行不完全一致。
	ConflictBeforeMismatch ConflictKind = "BEFORE_MISMATCH"
)

// Conflict 记录一条被判定为冲突而跳过的事件。
type Conflict struct {
	Seq     int64
	Key     string
	Op      Op
	Kind    ConflictKind
	Before  Row
	Current Row // 判定时副本中的当前行（行不存在时为 nil）
	Detail  string
}

// BatchResult 是一批事件的应用结果（仅在整批被接受时有效）。
type BatchResult struct {
	// Applied 成功应用的事件序号（按批内顺序）。
	Applied []int64
	// Conflicts 被分类为冲突而跳过的事件（按批内顺序）。
	Conflicts []Conflict
	// LastSeq 应用后已处理的连续序号上界。
	LastSeq int64
	// RowCount 应用后副本表行数。
	RowCount int
}
