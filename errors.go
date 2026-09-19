package ontology

import "errors"

var (
	// ErrClosed 表示分发器已关闭：Publish 不再生效，Subscribe 被拒绝。
	ErrClosed = errors.New("ontology: dispatcher closed")
	// ErrDuplicateID 表示订阅 ID 为空或已存在。
	ErrDuplicateID = errors.New("ontology: duplicate subscription id")
	// ErrInvalidBuffer 表示缓冲容量非法（小于 1）。
	ErrInvalidBuffer = errors.New("ontology: buffer must be >= 1")
	// ErrNotSubscribed 用于内部表达订阅不存在；取消操作对未知 ID 视为成功。
	ErrNotSubscribed = errors.New("ontology: subscription not found")
)
