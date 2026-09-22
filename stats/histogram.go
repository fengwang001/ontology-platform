// Package stats 持有表与列的统计信息，并只基于统计对象做选择率估计。
// 本包不提供任何行数据访问接口：估计过程物理上不可能扫描行。
package stats

import "errors"

// 三种统计异常，调用方用 errors.Is 区分。
var (
	// ErrStatMissing 表示某列完全没有登记统计信息。
	ErrStatMissing = errors.New("stats: column statistics missing")
	// ErrStatStale 表示统计行数与目录登记行数偏差超过阈值。
	ErrStatStale = errors.New("stats: statistics are stale")
	// ErrStatCorrupt 表示直方图桶计数之和不符或桶边界非递增。
	ErrStatCorrupt = errors.New("stats: histogram corrupt")
)

// StaleFraction 是过期判定阈值：相对偏差 > 10% 即视为过期。
const StaleFraction = 0.10

// Bucket 是等宽直方图的一个桶：(Lo, Hi] 区间（第一个桶含 Lo）内有 Count 行。
type Bucket struct {
	Lo    float64
	Hi    float64
	Count int64
}

// Histogram 是单列等宽直方图。
type Histogram struct {
	Buckets []Bucket
}

// Validate 检查直方图内部一致性：桶上界严格递增，且桶计数之和必须等于声称的行数。
// rows 为统计中声称的该列非空行数。
func (h *Histogram) Validate(rows int64) error {
	if h == nil || len(h.Buckets) == 0 {
		return nil
	}
	var sum int64
	prev := h.Buckets[0].Lo
	for i := range h.Buckets {
		b := &h.Buckets[i]
		if i > 0 && b.Lo < prev {
			return ErrStatCorrupt
		}
		if b.Hi <= b.Lo {
			return ErrStatCorrupt
		}
		if b.Count < 0 {
			return ErrStatCorrupt
		}
		sum += b.Count
		prev = b.Hi
	}
	if sum != rows {
		return ErrStatCorrupt
	}
	return nil
}

// bucketAt 返回值 v 落入的桶下标；v 落在所有桶之外时返回 -1。
func (h *Histogram) bucketAt(v float64) int {
	for i := range h.Buckets {
		b := &h.Buckets[i]
		if (i == 0 && v >= b.Lo && v <= b.Hi) || (v > b.Lo && v <= b.Hi) {
			return i
		}
	}
	return -1
}

// width 返回桶宽。
func (b *Bucket) width() float64 { return b.Hi - b.Lo }
