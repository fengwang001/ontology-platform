package stats

import "fmt"

// DefaultEqSelectivity 是缺少列统计时写死的等值选择率回退值。
const DefaultEqSelectivity = 0.1

// EqSelectivity 估计等值谓词 left.col = right.col 的选择率：
// sel = 1/max(ndv(left), ndv(right))。
// 任一侧统计缺失时返回 DefaultEqSelectivity 和包装后的 ErrStatsMissing。
func EqSelectivity(left, right *ColumnStat) (float64, error) {
	if left == nil || right == nil {
		key := "<nil>"
		switch {
		case left == nil && right == nil:
		case left == nil:
			key = right.Key()
		default:
			key = left.Key()
		}
		return DefaultEqSelectivity, fmt.Errorf("%w: %s", ErrStatsMissing, key)
	}
	if err := left.Validate(); err != nil {
		return 0, err
	}
	if err := right.Validate(); err != nil {
		return 0, err
	}
	d := left.NDV
	if right.NDV > d {
		d = right.NDV
	}
	if d <= 0 {
		return DefaultEqSelectivity, nil
	}
	return 1.0 / float64(d), nil
}

// RangeFraction 返回直方图中取值落在 [lo, hi] 区间内的行比例（桶内线性插值）。
// 无直方图时返回 ok=false，由调用方回退。
func (c *ColumnStat) RangeFraction(lo, hi float64) (float64, bool) {
	if c == nil || c.Hist == nil || len(c.Hist.Buckets) == 0 || c.RowCount <= 0 {
		return 0, false
	}
	var frac float64
	for _, b := range c.Hist.Buckets {
		l := maxf(b.Lo, lo)
		h := minf(b.Hi, hi)
		if h <= l {
			continue
		}
		width := b.Hi - b.Lo
		if width <= 0 {
			continue
		}
		frac += float64(b.Count) * (h - l) / width
	}
	return frac / float64(c.RowCount), true
}

// RangeSelectivity 估计范围/区间连接谓词的选择率：两侧重叠区间行比例之积。
func RangeSelectivity(left, right *ColumnStat, lo, hi float64) (float64, error) {
	if left == nil || right == nil {
		return DefaultEqSelectivity, fmt.Errorf("%w: 范围谓词缺列统计", ErrStatsMissing)
	}
	lf, ok1 := left.RangeFraction(lo, hi)
	rf, ok2 := right.RangeFraction(lo, hi)
	if !ok1 || !ok2 {
		return DefaultEqSelectivity, fmt.Errorf("%w: %s 或 %s 缺直方图",
			ErrStatsMissing, left.Key(), right.Key())
	}
	return lf * rf, nil
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
