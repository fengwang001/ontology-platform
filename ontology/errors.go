package ontology

import "errors"

// 非法输入类别：彼此互不相同、可区分。
var (
	ErrEmptyKey           = errors.New("empty key is not allowed")
	ErrUnknownToken       = errors.New("token is unknown")
	ErrTokenAlreadyUsed   = errors.New("token has already been consumed")
	ErrDeleteNotFound     = errors.New("cannot delete a row that does not exist")
	ErrTooManyTrackedKeys = errors.New("number of tracked keys exceeds the limit")
)

// ErrBackfillTooStale 表示回填被栅栏拒绝（合法但陈旧），不改变任何状态。
var ErrBackfillTooStale = errors.New("backfill token version is below the fence")
