package pncounter

import "errors"

// 可区分的拒绝原因：每个错误都对应唯一的哨兵值，调用方可用 errors.Is 判定。
var (
	// ErrInvalidReplicaCount：建集群时副本数非正。
	ErrInvalidReplicaCount = errors.New("pncounter: replica count must be positive")
	// ErrInvalidLimit：建集群时给定的单分量上限为 0。
	ErrInvalidLimit = errors.New("pncounter: component limit must be greater than zero")
	// ErrNoSuchReplica：副本编号越界或来源副本不存在（含 nil）。
	ErrNoSuchReplica = errors.New("pncounter: replica does not exist")
	// ErrNonPositiveDelta：增减量为 0（本实现使用无符号数，非正即 0）。
	ErrNonPositiveDelta = errors.New("pncounter: delta must be positive")
	// ErrComponentOverflow：操作会使对应分量超过集群配置的上限。
	ErrComponentOverflow = errors.New("pncounter: component would exceed limit")
	// ErrInvariantViolation：自检发现内部不变量被破坏，或跨集群合并且向量长度不一致。
	ErrInvariantViolation = errors.New("pncounter: invariant violation")
)
