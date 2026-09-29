package tso

import "errors"

// 可区分的拒绝 / 失败原因。
var (
	// ErrInvalidConfig：L 或 W 非正。
	ErrInvalidConfig = errors.New("tso: L and W must be positive")
	// ErrInvalidCount：n 非正或大于 L。
	ErrInvalidCount = errors.New("tso: n must be in [1, L]")
	// ErrNotLeader：向从节点请求时间戳。
	ErrNotLeader = errors.New("tso: node is not leader")
	// ErrTermNotHigher：以不大于存量的任期接任。
	ErrTermNotHigher = errors.New("tso: takeover term must be greater than stored term")
	// ErrExceedsBound：续写仍越界，本次发放失败。
	ErrExceedsBound = errors.New("tso: allocation would exceed persisted high bound")
)
