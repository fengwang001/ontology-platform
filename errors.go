package ontology

import "errors"

// 构造校验错误，四种情况互相独立，可用 errors.Is 区分。
var (
	// ErrInvalidBuckets 表示桶数 n <= 0。
	ErrInvalidBuckets = errors.New("ontology: number of buckets must be positive")
	// ErrInvalidRange 表示区间下界不小于上界（lo >= hi）。
	ErrInvalidRange = errors.New("ontology: range must satisfy lo < hi")
	// ErrInvalidBound 表示 lo 或 hi 为 NaN 或 Inf。
	ErrInvalidBound = errors.New("ontology: bounds must be finite non-NaN numbers")
	// ErrMismatchedSpec 表示 Merge 双方的 (lo, hi, n) 不一致。
	ErrMismatchedSpec = errors.New("ontology: cannot merge histograms with different specs")
	// ErrNaN 表示 Add 收到 NaN 样本。
	ErrNaN = errors.New("ontology: NaN sample is not accepted")
)
