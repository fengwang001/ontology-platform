package alloc

import "errors"

// 分配模块的可判定哨兵错误。调用方可用 errors.Is 区分失败原因。
var (
	// ErrNoWeights 在权重列表为空时返回。
	ErrNoWeights = errors.New("alloc: weights must not be empty")
	// ErrNegativeWeight 在任一权重为负时返回。
	ErrNegativeWeight = errors.New("alloc: weights must not be negative")
	// ErrZeroTotalWeight 在所有权重之和为 0 时返回。
	ErrZeroTotalWeight = errors.New("alloc: total weight must be greater than zero")
	// ErrOverflow 在中间计算会溢出 int64 时返回。
	ErrOverflow = errors.New("alloc: integer overflow")
	// ErrInvalidParts 在 Split 的份数 n <= 0 时返回。
	ErrInvalidParts = errors.New("alloc: number of parts must be positive")
)
