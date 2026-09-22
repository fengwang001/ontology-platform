package ontology

// Count 返回当前已接受的样本数（不含被拒绝的 NaN）。
func (a *Accumulator) Count() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

// Skipped 返回被拒绝的 NaN 样本数。
func (a *Accumulator) Skipped() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skipped
}

// Mean 返回当前均值。
//
// 零个样本时返回 ErrNoSamples；统计量已被无穷样本污染时返回 ErrUnusable。
func (a *Accumulator) Mean() (float64, error) {
	s := a.snapshot()
	if s.broken {
		return 0, ErrUnusable
	}
	if s.count == 0 {
		return 0, ErrNoSamples
	}
	return s.mean, nil
}

// Variance 返回总体方差 m2/count。
//
// 零个样本时返回 ErrNoSamples；统计量不可用时返回 ErrUnusable。
// 返回值保证非负：全等样本时精确为 0。
func (a *Accumulator) Variance() (float64, error) {
	s := a.snapshot()
	if s.broken {
		return 0, ErrUnusable
	}
	if s.count == 0 {
		return 0, ErrNoSamples
	}
	return s.m2 / float64(s.count), nil
}

// SampleVariance 返回样本方差 m2/(count-1)（贝塞尔校正）。
//
// 零个样本时返回 ErrNoSamples；恰好一个样本时自由度为零，
// 返回可区分的 ErrZeroDegreesOfFreedom；统计量不可用时返回 ErrUnusable。
func (a *Accumulator) SampleVariance() (float64, error) {
	s := a.snapshot()
	if s.broken {
		return 0, ErrUnusable
	}
	if s.count == 0 {
		return 0, ErrNoSamples
	}
	if s.count == 1 {
		return 0, ErrZeroDegreesOfFreedom
	}
	return s.m2 / float64(s.count-1), nil
}
