package ontology

import "errors"

// 哨兵错误：调用方可用 errors.Is 区分拒绝原因。
var (
	ErrInvalidConfig  = errors.New("invalid config")
	ErrEmptyKey       = errors.New("empty key")
	ErrDuplicateKey   = errors.New("duplicate key")
	ErrUnsorted       = errors.New("snapshot not strictly sorted")
	ErrTooManyChanges = errors.New("change log exceeds limit")
)

// Is 让 Error 与对应类别的哨兵错误匹配。
func (e *Error) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	switch target {
	case ErrInvalidConfig:
		return e.Kind == KindInvalidConfig
	case ErrEmptyKey:
		return e.Kind == KindEmptyKey
	case ErrDuplicateKey:
		return e.Kind == KindDuplicateKey
	case ErrUnsorted:
		return e.Kind == KindUnsorted
	case ErrTooManyChanges:
		return e.Kind == KindTooManyChanges
	default:
		return false
	}
}

func reject(kind ErrorKind, msg string) error {
	return &Error{Kind: kind, Msg: msg}
}
