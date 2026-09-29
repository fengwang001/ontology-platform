package replication

import "errors"

var (
	// ErrInvalidArgument 参数非法（空名、负世代、负位点等）。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrUnknownReplica 引用了不存在的副本。
	ErrUnknownReplica = errors.New("unknown replica")
	// ErrUnknownGeneration 请求的世代在该副本的世代缓存中不存在。
	ErrUnknownGeneration = errors.New("unknown generation")
	// ErrLogDiverged 同一位点的日志条目不一致，发生分叉。
	ErrLogDiverged = errors.New("log diverged")
)
