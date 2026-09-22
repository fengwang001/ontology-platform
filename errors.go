package ontology

import "errors"

// 可判定的哨兵错误，调用方用 errors.Is 区分。
var (
	// ErrNoSamples 表示在零个样本上请求均值或方差。
	ErrNoSamples = errors.New("ontology: no samples")

	// ErrZeroDegreesOfFreedom 表示样本方差的自由度为零（样本数小于 2）。
	ErrZeroDegreesOfFreedom = errors.New("ontology: sample variance needs at least 2 samples")

	// ErrUnusable 表示统计量已不可用（有正负无穷样本参与，均值/二阶矩变为非有限值）。
	ErrUnusable = errors.New("ontology: statistics unusable after non-finite sample")

	// ErrNaNRejected 表示一个 NaN 样本被拒绝，未污染统计量。
	ErrNaNRejected = errors.New("ontology: NaN sample rejected")
)
