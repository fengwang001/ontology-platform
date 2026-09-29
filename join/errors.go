package join

import (
	"errors"
	"fmt"
)

// RejectError 表示一批操作因某条非法输入而被整体拒绝。
// 被拒绝的批不会改变两表，也不会追加任何变更日志。
type RejectError struct {
	// Index 为触发拒绝的操作在批内的序号（从 0 开始）。
	Index int
	// Op 为触发拒绝的操作。
	Op Op
	// Reason 为可区分的拒绝原因。
	Reason Reason
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("join: op[%d] (%s/%s side=%s id=%q key=%q) rejected: %s",
		e.Index, e.Op.Side, e.Op.Kind, kindOrInvalid(e.Op.Kind), e.Op.Row.ID, e.Op.Row.Key, e.Reason)
}

func kindOrInvalid(k Kind) string {
	switch k {
	case Insert, Delete:
		return k.String()
	default:
		return "invalid"
	}
}

// 哨兵错误，可用 errors.Is 区分拒绝原因。
var (
	errEmptyKey         = errors.New(string(ReasonEmptyKey))
	errEmptyID          = errors.New(string(ReasonEmptyID))
	errUnknownSide      = errors.New(string(ReasonUnknownSide))
	errUnknownKind      = errors.New(string(ReasonUnknownKind))
	errDuplicateID      = errors.New(string(ReasonDuplicateID))
	errIDNotFound       = errors.New(string(ReasonIDNotFound))
	errRowLimitExceeded = errors.New(string(ReasonRowLimitExceeded))
)

func reasonErr(r Reason) error {
	switch r {
	case ReasonEmptyKey:
		return errEmptyKey
	case ReasonEmptyID:
		return errEmptyID
	case ReasonUnknownSide:
		return errUnknownSide
	case ReasonUnknownKind:
		return errUnknownKind
	case ReasonDuplicateID:
		return errDuplicateID
	case ReasonIDNotFound:
		return errIDNotFound
	case ReasonRowLimitExceeded:
		return errRowLimitExceeded
	default:
		return errors.New(string(r))
	}
}

// Is 支持 errors.Is(err, ErrRowLimitExceeded) 等用法。
func (e *RejectError) Is(target error) bool {
	t, ok := target.(*RejectError)
	if !ok {
		return false
	}
	return e.Reason == t.Reason
}

// Unwrap 暴露按原因分类的哨兵错误。
func (e *RejectError) Unwrap() error { return reasonErr(e.Reason) }

// 可供外部 errors.Is 使用的分类错误。
var (
	ErrEmptyKey         = &RejectError{Reason: ReasonEmptyKey}
	ErrEmptyID          = &RejectError{Reason: ReasonEmptyID}
	ErrUnknownSide      = &RejectError{Reason: ReasonUnknownSide}
	ErrUnknownKind      = &RejectError{Reason: ReasonUnknownKind}
	ErrDuplicateID      = &RejectError{Reason: ReasonDuplicateID}
	ErrIDNotFound       = &RejectError{Reason: ReasonIDNotFound}
	ErrRowLimitExceeded = &RejectError{Reason: ReasonRowLimitExceeded}
)
