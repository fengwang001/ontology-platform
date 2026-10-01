package fetch

import "errors"

var (
	ErrInvalidPartitionCount = errors.New("fetch: partition count N must be >= 1")
	ErrInvalidTotalBudget    = errors.New("fetch: total byte budget W must be >= 1")
	ErrInvalidPartitionLimit = errors.New("fetch: per-partition byte limit P must be >= 1")
	ErrPartitionOutOfRange   = errors.New("fetch: partition index out of range")
	ErrInvalidMessageSize    = errors.New("fetch: message size must be >= 1")
	ErrNoData                = errors.New("fetch: no data available in any partition")
)

// Record 描述一次响应中从某个分区取走的一条消息。
type Record struct {
	Partition int
	Offset    int
	Size      int
	Payload   string
}

// Response 是一次拉取组装的完整结果，可据此精确复现内容与下次轮转起点。
type Response struct {
	Records         []Record
	TotalBytes      int
	PartitionBytes  map[int]int
	NextStart       int
	FirstOverBudget bool
}

// Snapshot 是组装器内部状态的一致性快照。
type Snapshot struct {
	ConsumePositions []int
	Pending          []int
	RotationStart    int
}
