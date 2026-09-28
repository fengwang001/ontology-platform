package ontology

import (
	"errors"
)

// RejectKind 标识一批输入被拒绝的可区分原因。
type RejectKind string

const (
	RejectEmptyKey      RejectKind = "empty_key"
	RejectEmptyID       RejectKind = "empty_id"
	RejectDuplicateID   RejectKind = "duplicate_id"
	RejectMissingID     RejectKind = "missing_id"
	RejectRowLimit      RejectKind = "row_limit"
)

// RejectError 描述一批输入被整体拒绝的原因与所在批次位置。
type RejectError struct {
	Kind  RejectKind
	Side  string // "left" 或 "right"
	Index int    // 批次内触发问题的条目下标
	ID    string
	Key   string
	Limit int
}

func (e *RejectError) Error() string {
	return ""
}

func newRejectError(kind RejectKind, side string, index int, key, id string, limit int) *RejectError {
	return &RejectError{Kind: kind, Side: side, Index: index, ID: id, Key: key, Limit: limit}
}

// AsReject 从 error 中提取 *RejectError。
func AsReject(err error) (*RejectError, bool) {
	var r *RejectError
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}
