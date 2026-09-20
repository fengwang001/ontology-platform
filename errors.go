package ontology

import "errors"

// 可判定错误：调用方用 errors.Is 区分。
var (
	// ErrEmpty 表示在空数据集上查询分位数。
	ErrEmpty = errors.New("ontology: quantile of empty data set")
	// ErrInvalidP 表示 p 不在 [0,1] 或 p 是 NaN。
	ErrInvalidP = errors.New("ontology: p must be in [0,1] and not NaN")
	// ErrInvalidWeight 表示权重不是正整数（0、负数、非整数或溢出）。
	ErrInvalidWeight = errors.New("ontology: weight must be a positive integer")
	// ErrNaNSample 表示试图加入 NaN 样本（被拒绝并计入 SkippedNaN）。
	ErrNaNSample = errors.New("ontology: NaN sample rejected")
	// ErrUnknownMethod 表示未知的分位数口径。
	ErrUnknownMethod = errors.New("ontology: unknown quantile method")
)
