package lag

// Row 是进入变更流的一行。同一分区内按 (SortKey, ID) 升序排列。
type Row struct {
	Partition string
	ID        string
	SortKey   int64
	Value     string
}

// ChangeKind 是变更日志条目的类型。
type ChangeKind string

const (
	// ChangeInsert 新行出现。前驱值可能为空（分区首行）或非空。
	ChangeInsert ChangeKind = "insert"
	// ChangeDelete 行被删除。下游按序应用后该标识不再存在。
	ChangeDelete ChangeKind = "delete"
	// ChangeUpdate 行仍在，但其前驱取值发生了修正。
	ChangeUpdate ChangeKind = "update"
)

// Change 是一条变更日志。
//
// HasPrev 为 false 时前驱严格为空（分区首行），与零值字符串 "" 严格区分；
// HasPrev 为 true 时 Prev 才是前驱行的取值。
type Change struct {
	Kind      ChangeKind
	Partition string
	ID        string
	SortKey   int64
	Value     string
	HasPrev   bool
	Prev      string
}
