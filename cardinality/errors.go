package cardinality

import "errors"

var (
	// ErrInvalidPrecision 表示精度超出允许范围 [MinPrecision, MaxPrecision]。
	ErrInvalidPrecision = errors.New("cardinality: invalid precision")
	// ErrInvalidThreshold 表示稀疏转稠密阈值非法（小于 1）。
	ErrInvalidThreshold = errors.New("cardinality: invalid threshold")
	// ErrEmptyKey 表示加入或撤回了空键。
	ErrEmptyKey = errors.New("cardinality: empty key")
	// ErrKeyNotFound 表示撤回了一个不存在的键。
	ErrKeyNotFound = errors.New("cardinality: key not found")
	// ErrVerifyMismatch 表示自检时批量重算结果与当前状态不一致。
	ErrVerifyMismatch = errors.New("cardinality: verify mismatch")
)
