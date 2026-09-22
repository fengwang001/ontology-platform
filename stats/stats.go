// Package stats 保存表/列的不可变统计快照并校验其完整性。
package stats

import (
	"errors"
	"fmt"
	"math"
)

// 三种统计异常，调用方用 errors.Is 区分。
var (
	ErrStatsMissing = errors.New("stats: 列统计缺失")
	ErrStatsStale   = errors.New("stats: 统计已过期")
	ErrStatsCorrupt = errors.New("stats: 统计已损坏")
)

// StaleRelativeThreshold 是统计行数相对目录行数允许的最大偏差。
const StaleRelativeThreshold = 0.10

// Bucket 是等宽直方图中的一个桶，区间为 [Lo, Hi)。
type Bucket struct {
	Lo    float64
	Hi    float64
	Count int64
}

// Histogram 是某列的等宽直方图。
type Histogram struct {
	Buckets []Bucket
}

// ColumnStat 是某张表某一列的统计快照。
type ColumnStat struct {
	Table    string
	Column   string
	NDV      int64
	RowCount int64 // 采集统计时该表的行数
	Hist     *Histogram
}

// Key 返回“表.列”标识，用于错误信息与日志。
func (c *ColumnStat) Key() string {
	if c == nil {
		return "<nil>"
	}
	return c.Table + "." + c.Column
}

// Validate 校验直方图：桶上界必须严格递增，桶计数之和必须等于行数。
func (h *Histogram) Validate(key string, rowCount int64) error {
	if h == nil {
		return nil
	}
	if len(h.Buckets) == 0 {
		return fmt.Errorf("%w: %s: 直方图为空", ErrStatsCorrupt, key)
	}
	var sum int64
	prev := math.Inf(-1)
	for i, b := range h.Buckets {
		if b.Lo < prev || b.Hi <= b.Lo {
			return fmt.Errorf("%w: %s: 桶 %d 边界非递增 [%g,%g)",
				ErrStatsCorrupt, key, i, b.Lo, b.Hi)
		}
		prev = b.Hi
		if b.Count < 0 {
			return fmt.Errorf("%w: %s: 桶 %d 计数为负", ErrStatsCorrupt, key, i)
		}
		sum += b.Count
	}
	tol := float64(rowCount) * 1e-6
	if math.Abs(float64(sum-rowCount)) > math.Max(tol, 1) {
		return fmt.Errorf("%w: %s: 桶计数之和 %d != 行数 %d",
			ErrStatsCorrupt, key, sum, rowCount)
	}
	return nil
}

// Validate 校验单列统计的完整性（不含过期判定，过期由 catalog 按目录行数判定）。
func (c *ColumnStat) Validate() error {
	if c == nil {
		return ErrStatsMissing
	}
	if c.NDV < 0 || c.RowCount < 0 {
		return fmt.Errorf("%w: %s: NDV/行数为负", ErrStatsCorrupt, c.Key())
	}
	if err := c.Hist.Validate(c.Key(), c.RowCount); err != nil {
		return err
	}
	return nil
}

// Stale 判定统计行数相对目录（权威）行数是否过期，并返回相对偏差。
func Stale(statRows, catalogRows int64) (bool, float64) {
	base := math.Max(float64(catalogRows), 1)
	d := math.Abs(float64(statRows-catalogRows)) / base
	return d > StaleRelativeThreshold, d
}
