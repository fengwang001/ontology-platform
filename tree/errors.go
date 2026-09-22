package tree

import "errors"

var (
	// ErrNotFound 表示删除的区间在树中不存在（多重集里一个副本都没有）。
	ErrNotFound = errors.New("tree: interval not found")
	// ErrLimitExceeded 表示已插入区间数达到配置上限。
	ErrLimitExceeded = errors.New("tree: interval count limit exceeded")
)

// LimitError 携带当前上限，便于调用方可判定地区分超限原因。
type LimitError struct {
	Limit int
}

func (e *LimitError) Error() string        { return "tree: interval count limit exceeded" }
func (e *LimitError) Is(target error) bool { return target == ErrLimitExceeded }
