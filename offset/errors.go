package offset

import "errors"

// 哨兵错误：调用方用 errors.Is 区分拒绝原因。
var (
	// ErrEmptyPartitionKey 分区键为空。
	ErrEmptyPartitionKey = errors.New("offset: empty partition key")
	// ErrPartitionNotFound 操作了一个未声明的分区。
	ErrPartitionNotFound = errors.New("offset: partition not declared")
	// ErrPartitionExists 重复声明同一分区。
	ErrPartitionExists = errors.New("offset: partition already declared")
	// ErrNonContiguousDelivery 投递位点不连续（不是期望的下一条）。
	ErrNonContiguousDelivery = errors.New("offset: non-contiguous delivery")
	// ErrOffsetOutOfRange 确认了从未投递的位点。
	ErrOffsetOutOfRange = errors.New("offset: ack of undelivered offset")
	// ErrOffsetRegression 确认位点小于已提交位点（回退）。
	ErrOffsetRegression = errors.New("offset: ack regresses below committed offset")
	// ErrInflightLimitExceeded 跨分区共享在途上限被占满。
	ErrInflightLimitExceeded = errors.New("offset: shared inflight limit exceeded")
	// ErrInvalidLimit 在途上限非法。
	ErrInvalidLimit = errors.New("offset: inflight limit must be positive")
)
