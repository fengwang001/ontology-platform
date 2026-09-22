package query

import "errors"

var (
	// ErrBatchLimit 批量查询区间数超过配置上限，拒绝执行且无副作用。
	ErrBatchLimit = errors.New("query: batch size limit exceeded")
)

// BatchLimitError 携带配置上限，便于可判定处理。
type BatchLimitError struct {
	Limit int
}

func (e *BatchLimitError) Error() string        { return "query: batch size limit exceeded" }
func (e *BatchLimitError) Is(target error) bool { return target == ErrBatchLimit }
