package txreassemble

// EventType 标识到达事件的种类。
type EventType int

const (
	// EventUnknown 占位零值，属于非法事件。
	EventUnknown EventType = iota
	// EventBegin 事务开始。
	EventBegin
	// EventWrite 向事务写入一行。
	EventWrite
	// EventCommit 提交事务：整体输出其全部缓冲行。
	EventCommit
	// EventRollback 回滚事务：丢弃其全部缓冲行。
	EventRollback
)

// Row 是事务携带的一行数据，内容对重组器不透明。
type Row struct {
	Key     string
	Payload []byte
}

// Event 是一条到达的事务事件。
// Write 事件通过 Row 携带行；其余事件忽略 Row。
type Event struct {
	Type EventType
	TxID string
	Row  Row
}

// CommittedTx 是一个已提交事务的整体输出，包含其全部行（按到达顺序）。
type CommittedTx struct {
	TxID      string
	CommitSeq int64 // 提交到达序号（从 1 开始）
	Rows      []Row
}
