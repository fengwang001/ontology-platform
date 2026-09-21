package walstore

import "errors"

// 可判定错误：调用方可用 errors.Is 区分。
var (
	// ErrEmptyBatch 表示提交了空批次，不会写入任何记录。
	ErrEmptyBatch = errors.New("walstore: empty batch")
	// ErrEmptyKey 表示批次中含有空串键。
	ErrEmptyKey = errors.New("walstore: empty key")
	// ErrClosed 表示在 Close 之后继续操作。
	ErrClosed = errors.New("walstore: store is closed")
)
