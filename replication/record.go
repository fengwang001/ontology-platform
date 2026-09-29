package replication

// LSN 是日志记录的递增序号（日志序列号）。
type LSN uint64

// RecordType 标识日志记录的类型。
type RecordType int

const (
	// RecordBegin 开启一个事务。
	RecordBegin RecordType = iota
	// RecordData 是事务内的一条数据变更。
	RecordData
	// RecordCommit 提交事务，解码器在此刻发出整个事务。
	RecordCommit
	// RecordAbort 中止事务，解码器丢弃该事务已缓存的记录。
	RecordAbort
)

// Record 是一条按事务交错的日志记录。
type Record struct {
	LSN     LSN
	TxnID   uint64
	Type    RecordType
	Payload string
}

// Transaction 是一个完整解码后待发出的事务。
type Transaction struct {
	ID        uint64
	StartLSN  LSN
	CommitLSN LSN
	Records   []Record
}
