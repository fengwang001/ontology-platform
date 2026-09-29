package logstore

import "errors"

// 写入/删除/读取可能返回的可区分错误原因。
var (
	ErrEmptyKey       = errors.New("logstore: key must not be empty")
	ErrValueTooLarge  = errors.New("logstore: block larger than segment size")
	ErrKeyNotFound    = errors.New("logstore: key not found")
	ErrSpaceExhausted = errors.New("logstore: space exhausted, no cleaner can free a segment")
)
