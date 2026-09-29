package fulljoin

import (
	"errors"
	"fmt"
)

// 三类互不相同的可判定错误，用 errors.Is 判别。
var (
	// ErrDuplicateID：同一侧重复插入已存在的行标识。
	ErrDuplicateID = errors.New("fulljoin: duplicate row id")
	// ErrRowNotFound：删除某侧当前不存在的行标识。
	ErrRowNotFound = errors.New("fulljoin: row id not found")
	// ErrEmptyKey：行的键为空。
	ErrEmptyKey = errors.New("fulljoin: empty key")
)

// ChangeError 携带出错的批次下标与具体变更，便于判定与定位。
type ChangeError struct {
	Index  int
	Change Change
	Err    error
}

func (e *ChangeError) Error() string {
	return fmt.Sprintf("fulljoin: change #%d %q rejected: %v", e.Index, formatChange(e.Change), e.Err)
}

func (e *ChangeError) Unwrap() error { return e.Err }

func formatChange(c Change) string {
	return fmt.Sprintf("%c%s/%s", c.Kind, c.Side, c.Row.ID)
}

// IsDuplicateID 报告错误是否为"重复插入行标识"。
func IsDuplicateID(err error) bool { return errors.Is(err, ErrDuplicateID) }

// IsRowNotFound 报告错误是否为"删除不存在的行标识"。
func IsRowNotFound(err error) bool { return errors.Is(err, ErrRowNotFound) }

// IsEmptyKey 报告错误是否为"键为空"。
func IsEmptyKey(err error) bool { return errors.Is(err, ErrEmptyKey) }
