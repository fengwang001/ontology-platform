package ontology

import "errors"

// 哨兵错误，调用方可用 errors.Is 判定。
var (
	// ErrNoSamples 表示在没有任何样本时请求统计量。
	ErrNoSamples = errors.New("ontology: no samples")
	// ErrTooFewSamples 表示样本数不足以计算样本方差（自由度为零）。
	ErrTooFewSamples = errors.New("ontology: sample variance requires at least 2 samples")
	// ErrUnavailable 表示统计量已被非有限样本（±Inf）污染，不可用。
	ErrUnavailable = errors.New("ontology: statistics unavailable (non-finite input)")
)
