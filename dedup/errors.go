package dedup

import "fmt"

// RejectError 表示整批被拒绝：状态不会发生任何变化。
type RejectError struct {
	// Reason 是机器可读的拒绝原因。
	Reason RejectReason
	// Index 是触发拒绝的记录在输入批次中的下标（-1 表示与具体记录无关）。
	Index int
	// Partition 是触发拒绝的分区（无意义时为 -1）。
	Partition int
	Message   string
}

func (e *RejectError) Error() string {
	if e.Message != "" {
		return string(e.Reason) + ": " + e.Message
	}
	return string(e.Reason)
}

// RejectReason 是可区分的拒绝原因。
type RejectReason string

const (
	ReasonEmptyBatch          RejectReason = "empty_batch"
	ReasonNegativePartition   RejectReason = "negative_partition"
	ReasonNegativeOffset      RejectReason = "negative_offset"
	ReasonEmptyKey            RejectReason = "empty_key"
	ReasonOffsetNotIncreasing RejectReason = "offset_not_strictly_increasing"
	ReasonTooManyPartitions   RejectReason = "too_many_partitions"
)

func reject(why RejectReason, index, partition int, format string, args ...any) *RejectError {
	return &RejectError{
		Reason:    why,
		Index:     index,
		Partition: partition,
		Message:   fmt.Sprintf(format, args...),
	}
}
