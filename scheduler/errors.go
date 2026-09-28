package scheduler

import "errors"

// 本文件定义调度器对外暴露的全部错误类别。
// 每一类非法输入对应一个互不相同、可区分的哨兵错误，调用方可使用 errors.Is 判定。
var (
	// ErrNilTransaction 表示提交了零值事务（序号为零且写集为空，或写集切片本身为 nil）。
	ErrNilTransaction = errors.New("scheduler: transaction must not be nil/zero")

	// ErrInvalidSequence 表示事务序号不是正整数。
	ErrInvalidSequence = errors.New("scheduler: transaction sequence must be a positive integer")

	// ErrSequenceGap 表示序号没有从 1 开始或未按提交顺序连续递增。
	ErrSequenceGap = errors.New("scheduler: transaction sequence must start at 1 and be consecutive")

	// ErrEmptyWriteSet 表示事务的写键集合为空。
	ErrEmptyWriteSet = errors.New("scheduler: transaction write set must not be empty")

	// ErrEmptyWriteKey 表示事务的写键集合中包含空字符串键。
	ErrEmptyWriteKey = errors.New("scheduler: transaction write set must not contain empty keys")

	// ErrTooManyTransactions 表示接受该事务将使事务总数超过 MaxTransactions。
	ErrTooManyTransactions = errors.New("scheduler: accepted transaction count would exceed MaxTransactions")

	// ErrInvalidParallelism 表示并行度上限不是正整数。
	ErrInvalidParallelism = errors.New("scheduler: maxParallel must be a positive integer")
)
