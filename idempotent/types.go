package idempotent

// Record 是至少一次投递的一条记录，由分区与位点唯一标识。
type Record struct {
	Partition int
	Offset    int64
	Key       string
	Value     int64
}

// Batch 是一次投递的批次。
type Batch struct {
	ID      string
	Records []Record
}

// Outcome 是一批记录的处理结果。
type Outcome struct {
	Applied   int
	Duplicate int
	Rejected  bool
	Err       error
}

// Snapshot 是持久化状态的只读快照。
type Snapshot struct {
	Results       map[string]int64
	Watermarks    map[int]int64
	Duplicates    int64
	MaxPartitions int
}
