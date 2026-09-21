package ontology

import "errors"

var (
	// ErrNoSamples 表示在零个样本时请求均值或方差。
	ErrNoSamples = errors.New("ontology: no samples")

	// ErrInsufficientSamples 表示样本数不足以计算样本方差（自由度为零）。
	// 它与 ErrNoSamples 是不同的哨兵错误，调用方可以用 errors.Is 区分。
	ErrInsufficientSamples = errors.New("ontology: sample variance requires at least 2 samples")

	// ErrStatsUnavailable 表示统计量已被 ±Inf 样本污染，不再可用。
	ErrStatsUnavailable = errors.New("ontology: statistics unavailable (non-finite sample seen)")

	// ErrNaNSample 表示向累加器喂入了 NaN，样本被拒绝并计入跳过计数。
	ErrNaNSample = errors.New("ontology: NaN sample rejected")
)
