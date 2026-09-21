package ontology

import "errors"

var (
	// ErrNoSamples 表示在零个样本时读取均值或方差。
	ErrNoSamples = errors.New("ontology: no samples")

	// ErrNoDegreesOfFreedom 表示只有一个样本时读取样本方差
	// （自由度为零，样本方差无定义）。
	ErrNoDegreesOfFreedom = errors.New("ontology: sample variance needs at least 2 samples")

	// ErrUnavailable 表示统计量已不可用：有正负无穷样本参与统计，
	// 均值或方差已退化为无穷或 NaN，不再返回给调用方。
	ErrUnavailable = errors.New("ontology: statistics unavailable (non-finite sample seen)")

	// ErrNaN 表示向累加器喂入了 NaN 样本，该样本被拒绝并计入
	// 跳过计数，不污染任何统计量。
	ErrNaN = errors.New("ontology: NaN sample rejected")
)
