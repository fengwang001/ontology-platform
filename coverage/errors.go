package coverage

import "errors"

// 可判定错误：调用方可用 errors.Is 精确识别。
var (
	// ErrEmptyInterval 表示 hi <= lo；区间必须满足 lo < hi。
	ErrEmptyInterval = errors.New("coverage: empty interval, require lo < hi")

	// ErrNotTracked 表示要 Remove 的区间此前未被添加（或已被全部移除）。
	ErrNotTracked = errors.New("coverage: interval is not tracked")

	// ErrOverRemoved 表示一次 Remove 会使该区间的层数变为负数。
	ErrOverRemoved = errors.New("coverage: interval would be removed more times than added")
)
