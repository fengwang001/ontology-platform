package tier

import (
	"errors"
	"math"
)

// 跨包共享的错误哨兵。hold、rollup 通过 tier 引用，保证错误身份唯一。
var (
	ErrInvalidArgument = errors.New("invalid argument")       // 参数非法
	ErrExpired         = errors.New("point expired")          // 超过最高保留年龄
	ErrOverflow        = errors.New("aggregate overflow")     // count/sum 超 int64
	ErrCapacity        = errors.New("capacity exhausted")     // 需新增单位而容量已满
	ErrClockRollback   = errors.New("clock cannot roll back") // 时钟回退
	ErrAborted         = errors.New("advance aborted")        // Advance 中途失败
	ErrDuplicateHold   = errors.New("duplicate hold id")      // 保全 id 重复
	ErrHoldNotFound    = errors.New("hold not found")         // 保全不存在
	ErrPermission      = errors.New("permission denied")      // 非 owner 且非 admin
)

// 桶宽（毫秒）。
const (
	MinuteMS int64 = 60_000
	HourMS   int64 = 3_600_000
)

// 规格边界。
const (
	MaxClock int64 = 10_000_000_000_000 // 10^13
	MaxValue int64 = 1_000_000_000_000  // |v| <= 10^12
)

// Point 是 L0 原始点。
type Point struct {
	TS int64
	V  int64
}

// Bucket 是 L1/L2 聚合桶，四个字段均为 int64。
type Bucket struct {
	Count int64
	Sum   int64
	Min   int64
	Max   int64
}

// MinuteOf 返回时间戳所属分钟桶 key：m = floor(ts/60000)。
func MinuteOf(ts int64) int64 { return ts / MinuteMS }

// HourOf 返回时间戳所属小时桶 key：h = floor(ts/3600000)。
func HourOf(ts int64) int64 { return ts / HourMS }

// MinuteRange 返回分钟桶 m 的半开区间 [from,to)。
func MinuteRange(m int64) (from, to int64) { return m * MinuteMS, (m + 1) * MinuteMS }

// HourRange 返回小时桶 h 的半开区间 [from,to)。
func HourRange(h int64) (from, to int64) { return h * HourMS, (h + 1) * HourMS }

// addInt64 返回 a+b，并在溢出时返回 ErrOverflow。
func addInt64(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// AddPoint 把单个点并入桶，count/sum 溢出时返回 ErrOverflow 且不修改 b。
func (b *Bucket) AddPoint(v int64) error {
	count, err := addInt64(b.Count, 1)
	if err != nil {
		return err
	}
	sum, err := addInt64(b.Sum, v)
	if err != nil {
		return err
	}
	b.Count, b.Sum = count, sum
	if count == 1 {
		b.Min, b.Max = v, v
	} else {
		if v < b.Min {
			b.Min = v
		}
		if v > b.Max {
			b.Max = v
		}
	}
	return nil
}

// Merge 把 other 并入 b；other 为空（Count==0）时无操作。
// count/sum 溢出时返回 ErrOverflow 且不修改 b。
func (b *Bucket) Merge(other Bucket) error {
	if other.Count == 0 {
		return nil
	}
	count, err := addInt64(b.Count, other.Count)
	if err != nil {
		return err
	}
	sum, err := addInt64(b.Sum, other.Sum)
	if err != nil {
		return err
	}
	if b.Count == 0 {
		b.Min, b.Max = other.Min, other.Max
	} else {
		if other.Min < b.Min {
			b.Min = other.Min
		}
		if other.Max > b.Max {
			b.Max = other.Max
		}
	}
	b.Count, b.Sum = count, sum
	return nil
}

// IntervalsOverlap 判定两个半开区间 [aFrom,aTo) 与 [bFrom,bTo) 是否相交。
// 半开：恰在端点相接（aTo==bFrom 等）不算相交。
func IntervalsOverlap(aFrom, aTo, bFrom, bTo int64) bool {
	return aFrom < bTo && bFrom < aTo
}
