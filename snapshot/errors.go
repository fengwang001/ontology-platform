package snapshot

import "errors"

// 差分输入的可区分错误类别。
var (
	ErrInvalidConfig  = errors.New(ReasonInvalidConfig)
	ErrDuplicateKey   = errors.New(ReasonDuplicateKey)
	ErrUnsorted       = errors.New(ReasonUnsorted)
	ErrTooManyChanges = errors.New(ReasonTooManyChanges)
)

const (
	ReasonInvalidConfig  = "invalid config"
	ReasonDuplicateKey   = "duplicate key"
	ReasonUnsorted       = "snapshot not strictly sorted"
	ReasonTooManyChanges = "change log exceeds limit"
)

// DiffError 携带互不相同、可区分的拒绝原因。
type DiffError struct {
	Reason string
	detail string
}

func (e *DiffError) Error() string {
	return e.Reason + ": " + e.detail
}

// Is 使 errors.Is 可按可区分的错误类别匹配。
func (e *DiffError) Is(target error) bool {
	switch target {
	case ErrInvalidConfig:
		return e.Reason == ReasonInvalidConfig
	case ErrDuplicateKey:
		return e.Reason == ReasonDuplicateKey
	case ErrUnsorted:
		return e.Reason == ReasonUnsorted
	case ErrTooManyChanges:
		return e.Reason == ReasonTooManyChanges
	}
	return false
}

func newDiffError(reason, detail string) *DiffError {
	return &DiffError{Reason: reason, detail: detail}
}
