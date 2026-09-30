package fojoin

import (
	"errors"
	"fmt"
)

// 三类互不相同的可判定非法输入，可用 errors.Is 判定。
var (
	// ErrEmptyKey 表示变更的键为空。
	ErrEmptyKey = errors.New("fojoin: empty key")
	// ErrDuplicateInsert 表示重复插入同一侧同一键上已存在的行标识。
	ErrDuplicateInsert = errors.New("fojoin: duplicate insert")
	// ErrMissingDelete 表示删除不存在的行标识。
	ErrMissingDelete = errors.New("fojoin: delete of missing row")
	// ErrUnknownOp 表示变更动作不是 Insert / Delete。
	ErrUnknownOp = errors.New("fojoin: unknown op")
	// ErrUnknownSide 表示变更侧不是 Left / Right。
	ErrUnknownSide = errors.New("fojoin: unknown side")
)

// RejectError 描述一条被拒绝的变更，Kind 可用 errors.Is 与
// ErrEmptyKey / ErrDuplicateInsert / ErrMissingDelete 判定。
type RejectError struct {
	Kind   error
	Index  int // 在批内的下标
	Change Change
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("%v: batch[%d] %s %s key=%q row=%q",
		e.Kind, e.Index, e.Change.Side, e.Change.Op, e.Change.Key, e.Change.RowID)
}

// Unwrap 暴露 Kind 供 errors.Is 判定。
func (e *RejectError) Unwrap() error { return e.Kind }
