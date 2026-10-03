// Package rollup 定义分层存储共享的时间常量、聚合桶与溢出检查运算。
package rollup

import "errors"

// Minute/Hour 为一分钟与一小时的毫秒数；ts 属于 m=ts/Minute、h=ts/Hour。
const (
	Minute int64 = 60000
	Hour   int64 = 3600000
)

// Bucket 是分钟桶与小时桶的统一内容，所有字段均为 int64。
type Bucket struct {
	Count int64
	Sum   int64
	Min   int64
	Max   int64
}

// Point 是 L0 原始点。
type Point struct {
	TS int64
	V  int64
}

// ErrOverflow 表示合并后 Sum 超出 int64 范围。
var ErrOverflow = errors.New("rollup: sum overflow")

// MinuteOf / HourOf 返回时间戳所属的分钟桶号 / 小时桶号。
func MinuteOf(ts int64) int64 { return ts / Minute }
func HourOf(ts int64) int64   { return ts / Hour }

// MinuteRange / HourRange 返回桶号对应的半开毫秒区间 [start, end)。
func MinuteRange(m int64) (int64, int64) { return m * Minute, (m + 1) * Minute }
func HourRange(h int64) (int64, int64)   { return h * Hour, (h + 1) * Hour }

// addChecked 做带溢出检查的 int64 加法。
func addChecked(a, b int64) (int64, error) {
	sum := a + b
	if (a > 0 && b > 0 && sum < 0) || (a < 0 && b < 0 && sum >= 0) {
		return 0, ErrOverflow
	}
	return sum, nil
}

// single 用一个原始值构造单点桶。
func single(v int64) Bucket {
	return Bucket{Count: 1, Sum: v, Min: v, Max: v}
}

// mergeBucket 把 src 并入 dst；Count 与 Sum 做溢出检查。
// 空桶（Count==0）不参与 min/max，便于安全地从空值开始折叠。
func mergeBucket(dst, src Bucket) (Bucket, error) {
	if src.Count == 0 {
		return dst, nil
	}
	count, err := addChecked(dst.Count, src.Count)
	if err != nil {
		return Bucket{}, err
	}
	sum, err := addChecked(dst.Sum, src.Sum)
	if err != nil {
		return Bucket{}, err
	}
	if dst.Count == 0 {
		return Bucket{Count: count, Sum: sum, Min: src.Min, Max: src.Max}, nil
	}
	minV := dst.Min
	if src.Min < minV {
		minV = src.Min
	}
	maxV := dst.Max
	if src.Max > maxV {
		maxV = src.Max
	}
	return Bucket{Count: count, Sum: sum, Min: minV, Max: maxV}, nil
}

// AddPoint 把单个原始值并入桶。
func AddPoint(b Bucket, v int64) (Bucket, error) {
	return mergeBucket(b, single(v))
}

// Merge 是 mergeBucket 的导出版本，供 tier 层折叠使用。
func Merge(dst, src Bucket) (Bucket, error) { return mergeBucket(dst, src) }
