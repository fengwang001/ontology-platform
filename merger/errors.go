package merger

import "fmt"

// ErrorCode 标识整批拒绝的可区分原因。
type ErrorCode string

const (
	// ErrorUnknownColumn 事件引用了表结构之外的列。
	ErrorUnknownColumn ErrorCode = "unknown_column"
	// ErrorInsertMissingColumn 插入事件未给出全部列。
	ErrorInsertMissingColumn ErrorCode = "insert_missing_column"
	// ErrorUnknownEventType 事件类型既不是插入也不是更新。
	ErrorUnknownEventType ErrorCode = "unknown_event_type"
	// ErrorEmptyKey 主键为空字符串。
	ErrorEmptyKey ErrorCode = "empty_key"
	// ErrorEmptyUpdate 更新事件不包含任何变更列。
	ErrorEmptyUpdate ErrorCode = "empty_update"
	// ErrorChangeMirrorColumnMismatch 更新的变更列集合与变更前镜像列集合不一致。
	ErrorChangeMirrorColumnMismatch ErrorCode = "change_mirror_column_mismatch"
	// ErrorKeyNotFound 更新事件引用了表中不存在（本批也未插入）的键。
	ErrorKeyNotFound ErrorCode = "key_not_found"
	// ErrorKeyAlreadyExists 插入事件的键在表中或本批内已存在。
	ErrorKeyAlreadyExists ErrorCode = "key_already_exists"
	// ErrorBeforeImageMismatch 变更前镜像的值与该列批内首次被更新时的实际值不符。
	ErrorBeforeImageMismatch ErrorCode = "before_image_mismatch"
)

// BatchError 描述导致整批被拒绝的原因，互不相同的 Code 保证可区分。
type BatchError struct {
	Code   ErrorCode
	Index  int
	Key    string
	Column string
	Detail string
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("batch rejected: code=%s event_index=%d key=%q column=%q: %s",
		e.Code, e.Index, e.Key, e.Column, e.Detail)
}

func batchError(code ErrorCode, index int, key, column, detail string) *BatchError {
	return &BatchError{Code: code, Index: index, Key: key, Column: column, Detail: detail}
}
