package cdc

import (
	"errors"
	"fmt"
)

// 批被拒绝的可区分原因，可用 errors.Is 判定。
var (
	// ErrInvalidPrimaryKey 主键非法（为空、超长或新旧主键缺失）。
	ErrInvalidPrimaryKey = errors.New("cdc: invalid primary key")
	// ErrKeyAlreadyExists 目标主键已存在（插入冲突或主键变更后撞键）。
	ErrKeyAlreadyExists = errors.New("cdc: key already exists")
	// ErrKeyNotFound 目标主键不存在（更新或删除不存在的键）。
	ErrKeyNotFound = errors.New("cdc: key not found")
	// ErrTooManyRows 一批变更的行数超过上限。
	ErrTooManyRows = errors.New("cdc: too many rows in batch")
)

// RejectError 描述一批变更被拒绝的原因，携带批内定位信息。
// 被拒绝的批不会产生任何事件，也不会改变源表与下游视图。
type RejectError struct {
	Reason error // 上述哨兵错误之一，可用 errors.Is 区分
	Index  int   // 触发拒绝的变更在批内的下标；行数超限时为 -1
	Key    string
	Detail string // 判定依据的可读描述
}

// Error 实现 error 接口。
func (e *RejectError) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("%v: batch[%d] key=%q: %s", e.Reason, e.Index, e.Key, e.Detail)
	}
	return fmt.Sprintf("%v: %s", e.Reason, e.Detail)
}

// Unwrap 暴露原因，配合 errors.Is 使用。
func (e *RejectError) Unwrap() error { return e.Reason }
