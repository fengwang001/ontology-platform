package ontology

// Mean 返回当前样本均值。
//
// 零个样本时返回 ErrNoSamples；统计量已不可用（有无穷样本参与）
// 时返回 ErrUnavailable，绝不把 NaN 悄悄返回给调用方。
func (a *Accumulator) Mean() (float64, error) {
	count, mean, _, _, unusable := a.snapshot()
	if unusable {
		return 0, ErrUnavailable
	}
	if count == 0 {
		return 0, ErrNoSamples
	}
	return mean, nil
}

// Variance 返回总体方差 m2/n。
//
// 零个样本时返回 ErrNoSamples；统计量不可用时返回 ErrUnavailable。
// 方差绝不返回负数：全部样本相同时返回精确的 0。
func (a *Accumulator) Variance() (float64, error) {
	count, _, m2, _, unusable := a.snapshot()
	if unusable {
		return 0, ErrUnavailable
	}
	if count == 0 {
		return 0, ErrNoSamples
	}
	return clampVariance(m2 / float64(count)), nil
}

// SampleVariance 返回样本方差 m2/(n-1)。
//
// 零个样本时返回 ErrNoSamples；恰好一个样本时自由度为零，
// 返回与 ErrNoSamples 可区分的 ErrNoDegreesOfFreedom；
// 统计量不可用时返回 ErrUnavailable。
func (a *Accumulator) SampleVariance() (float64, error) {
	count, _, m2, _, unusable := a.snapshot()
	if unusable {
		return 0, ErrUnavailable
	}
	if count == 0 {
		return 0, ErrNoSamples
	}
	if count == 1 {
		return 0, ErrNoDegreesOfFreedom
	}
	return clampVariance(m2 / float64(count-1)), nil
}

// clampVariance 把浮点舍入可能产生的微小负值钳制到 0，
// 保证方差在数学非负的前提下返回值也绝不非负。
func clampVariance(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
