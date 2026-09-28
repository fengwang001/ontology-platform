package committer

import "errors"

// 哨兵错误，调用方用 errors.Is 区分拒绝原因。
var (
	// ErrEmptyPartitionKey 分区键为空。
	ErrEmptyPartitionKey = errors.New("committer: empty partition key")
	// ErrPartitionNotDeclared 操作了一个未声明的分区。
	ErrPartitionNotDeclared = errors.New("committer: partition not declared")
	// ErrPartitionAlreadyDeclared 重复声明同一分区。
	ErrPartitionAlreadyDeclared = errors.New("committer: partition already declared")
	// ErrNonContiguousDelivery 投递位点不与期望的下一条投递位点连续。
	ErrNonContiguousDelivery = errors.New("committer: non-contiguous delivery")
	// ErrAckOutOfRange 确认了未投递的位点（越界）。
	ErrAckOutOfRange = errors.New("committer: ack of undelivered offset")
	// ErrAckRegression 确认位点小于已提交位点（回退）。
	ErrAckRegression = errors.New("committer: ack regresses below committed offset")
	// ErrInFlightLimitExceeded 跨分区共享在途上限被占满，整体拒绝。
	ErrInFlightLimitExceeded = errors.New("committer: shared in-flight limit exceeded")
)
