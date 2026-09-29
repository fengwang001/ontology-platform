package median

import "errors"

// 互相区分的错误类别。调用方可用 errors.Is 判别。
var (
	// ErrInvalidArgument 非法参数：如空批次、未知操作类型、nil 接收者。
	ErrInvalidArgument = errors.New("median: invalid argument")
	// ErrEmpty 空多重集上查询中位数。
	ErrEmpty = errors.New("median: multiset is empty")
	// ErrNotFound 撤回一个当前有效多重集中不存在的值。
	ErrNotFound = errors.New("median: value not present in multiset")
	// ErrLimitExceeded 加入后元素个数超过配置上限。
	ErrLimitExceeded = errors.New("median: multiset size limit exceeded")
)
