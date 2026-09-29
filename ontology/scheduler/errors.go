package scheduler

import "errors"

// 可区分的错误类别。使用 errors.Is 判定；具体消息通过 %w 附带上下文。
var (
	// ErrInvalidArgument：参数本身非法（nil、空键、非法映射等）。
	ErrInvalidArgument = errors.New("scheduler: invalid argument")
	// ErrSeqNotConsecutive：事务序号不是从 1 开始严格连续递增。
	ErrSeqNotConsecutive = errors.New("scheduler: transaction sequence is not consecutive from 1")
	// ErrWriteSetEmpty：事务写集为空（去重后没有任何键）。
	ErrWriteSetEmpty = errors.New("scheduler: transaction write set is empty")
	// ErrWriteSetEmptyKey：事务写集中包含空字符串键。
	ErrWriteSetEmptyKey = errors.New("scheduler: transaction write set contains empty key")
	// ErrTxLimitExceeded：事务总数超过 MaxTransactions 上限。
	ErrTxLimitExceeded = errors.New("scheduler: transaction count exceeds limit")
	// ErrTxNotFound：查询的事务序号不存在。
	ErrTxNotFound = errors.New("scheduler: transaction not found")
	// ErrInvalidParallel：并行度上限不是正整数。
	ErrInvalidParallel = errors.New("scheduler: max parallelism must be positive")
)
