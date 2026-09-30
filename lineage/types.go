package lineage

// ShardID 标识一个分片。
type ShardID int64

// Clock 是单调推进的逻辑时钟。
type Clock int64

// WorkerID 标识消费工作者。
type WorkerID string

// ShardInfo 为查询返回的分片快照信息。
type ShardInfo struct {
	ID          ShardID
	Lo          int
	Hi          int
	Closed      bool
	EndPosition int64
	Appended    int64
	Committed   int64
	Drained     bool
	Parents     []ShardID
	Holder      WorkerID
	LeaseValid  bool
	ExpiresAt   Clock
}

// AppendResult 为追加结果。
type AppendResult struct {
	Shard    ShardID
	Position int64
}
