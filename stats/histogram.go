package stats

import "errors"

// ErrCorruptHistogram 表示等宽直方图内部不自洽：
// 桶边界非严格递增，或桶计数之和与声称行数不一致。
var ErrCorruptHistogram = errors.New("corrupt histogram")

// Histogram 是等宽直方图：Bounds 长度为桶数+1，Counts 长度为桶数。
type Histogram struct {
	Bounds []float64
	Counts []int64
}

// Validate 校验边界严格递增且桶计数之和恰为 rows。
func (h Histogram) Validate(rows int64) error {
	if len(h.Bounds) != len(h.Counts)+1 {
		return ErrCorruptHistogram
	}
	for i := 1; i < len(h.Bounds); i++ {
		if h.Bounds[i] <= h.Bounds[i-1] {
			return ErrCorruptHistogram
		}
	}
	var sum int64
	for _, c := range h.Counts {
		sum += c
	}
	if sum != rows {
		return ErrCorruptHistogram
	}
	return nil
}

// Buckets 返回桶数。
func (h Histogram) Buckets() int { return len(h.Counts) }
