package tso

import "errors"

var (
	// ErrInvalidConfig：L 或 W 非正。
	ErrInvalidConfig = errors.New("tso: invalid config")
	// ErrInvalidCount：n 非正或大于 L。
	ErrInvalidCount = errors.New("tso: invalid timestamp count")
	// ErrNotLeader：向从节点请求时间戳。
	ErrNotLeader = errors.New("tso: not leader")
	// ErrStaleTerm：以不大于存量的任期接任。
	ErrStaleTerm = errors.New("tso: stale term")
	// ErrTermSuperseded：写入时发现共享存储中的任期已被超过。
	ErrTermSuperseded = errors.New("tso: term superseded in storage")
	// ErrPersistUnavailable：续写因注入故障失败，且已越过已持久化上界。
	ErrPersistUnavailable = errors.New("tso: cannot persist new upper bound")
	// ErrInjectedWriteFailure：测试注入的普通持久化失败（存储内容不变）。
	ErrInjectedWriteFailure = errors.New("tso: injected storage write failure")
)
