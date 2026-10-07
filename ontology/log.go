package ontology

// OpKind 标识一次写操作或查询的类型。
type OpKind string

const (
	OpCreateObject      OpKind = "CREATE_OBJECT"
	OpAddHistory        OpKind = "ADD_HISTORY"
	OpDeleteObject      OpKind = "DELETE_OBJECT"
	OpRestoreObject     OpKind = "RESTORE_OBJECT"
	OpDeleteRecord      OpKind = "DELETE_RECORD"
	OpUndeleteRecord    OpKind = "UNDELETE_RECORD"
	OpQueryVisibleValue OpKind = "QUERY_VISIBLE_VALUE"
)

// TriCondition 记录一次操作据以判定的三层条件取值快照。
type TriCondition struct {
	ObjectAlive bool // 第一层：对象未处于整体删除状态
	RecordAlive bool // 第二层：目标记录未被单独删除（无目标记录时为 false）
	IsCurrent   bool // 第三层：目标记录在时间维度上为最新有效记录
}

// LogEntry 是操作日志条目：输入、输出与三层条件取值。
type LogEntry struct {
	Seq      uint64
	Op       OpKind
	ObjectID ObjectID
	RecordID RecordID
	Property string // 查询/追加历史的目标属性
	At       int64  // 查询时间点（仅查询）
	Err      error  // 输出：错误（nil 表示成功）
	Result   *QueryResult
	Cond     TriCondition
}
