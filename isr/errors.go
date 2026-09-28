package isr

import "errors"

// 可区分的拒绝原因。所有被拒绝的操作都不会改变任何状态。
var (
	// ErrInvalidArgument 参数非法（空副本名、零阈值等）。
	ErrInvalidArgument = errors.New("isr: invalid argument")
	// ErrUnknownReplica 副本不属于本分区副本集。
	ErrUnknownReplica = errors.New("isr: unknown replica")
	// ErrNotLeader 写入操作不是由领导者发起的。
	ErrNotLeader = errors.New("isr: replica is not the leader")
	// ErrInvalidOffset 位点非法：回退或超过领导者日志结束位点。
	ErrInvalidOffset = errors.New("isr: invalid offset")
	// ErrClockMovedBack 提供的时钟早于该副本上一次观察到的时间。
	ErrClockMovedBack = errors.New("isr: clock moved back")
	// ErrReplicaAlreadyExists 构造时同一副本被重复注册。
	ErrReplicaAlreadyExists = errors.New("isr: replica already exists")
	// ErrLeaderMissing 构造参数中缺少领导者。
	ErrLeaderMissing = errors.New("isr: leader missing from replica set")
)
