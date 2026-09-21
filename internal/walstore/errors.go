package walstore

import "errors"

// ErrEmptyBatch 表示向 Commit 传入了空 map。
var ErrEmptyBatch = errors.New("walstore: empty batch")

// ErrEmptyKey 表示批次中包含空字符串键。
var ErrEmptyKey = errors.New("walstore: empty key")
