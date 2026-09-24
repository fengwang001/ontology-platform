// Package bidx 实现位点↔时间戳双向索引：原始 ts 序列、前缀最大值、
// Append/TSAt/SafeOff（二分）。依赖 tsl。本包不做并发控制，由上层 api 负责。
package bidx

import (
	"errors"
	"sync/atomic"

	"ontology/tsl"
)

// 三类可判定故障，互不相同。
var (
	ErrOutOfRange = errors.New("bidx: offset out of range")  // TSAt 越界
	ErrEmpty      = errors.New("bidx: empty log")            // SafeOff 时 N==0
	ErrFull       = errors.New("bidx: max records exceeded") // Append 超 maxRecords
)

// Index 是双向索引。lastCompares 记录最近一次 SafeOff 二分的比较次数，
// 非导出，不出现在任何公开接口；atomic 保证并发只读下 -race 干净。
type Index struct {
	recs         []tsl.Rec // 按位点顺序的原始记录
	pm           []int64   // 前缀最大值，非递减
	maxRecords   int
	lastCompares atomic.Int64
}

// New 创建容量上限为 maxRecords 的索引。
func New(maxRecords int) *Index {
	return &Index{maxRecords: maxRecords}
}

// Len 返回已追加条数。
func (x *Index) Len() int { return len(x.recs) }

// Append 追加一条 ts，位点按 0,1,2,... 严格递增分配。
// 超过 maxRecords 时报 ErrFull 且状态不变。
func (x *Index) Append(ts int64) error {
	if len(x.recs) >= x.maxRecords {
		return ErrFull
	}
	off := int64(len(x.recs))
	pm := ts
	if off > 0 {
		pm = tsl.NextPM(x.pm[off-1], ts)
	}
	x.recs = append(x.recs, tsl.Rec{Off: off, Ts: ts})
	x.pm = append(x.pm, pm)
	return nil
}

// TSAt 返回第 off 条的 ts。off<0 或 off>=N 报 ErrOutOfRange 且状态不变。
func (x *Index) TSAt(off int64) (int64, error) {
	if off < 0 || off >= int64(len(x.recs)) {
		return 0, ErrOutOfRange
	}
	return x.recs[off].Ts, nil
}

// SafeOff 返回满足 pm[o] <= T 的最大位点 o（pm 非递减，二分求解）。
// pm[0] > T 时返回 -1, false, nil；空日志报 ErrEmpty。
func (x *Index) SafeOff(T int64) (int64, bool, error) {
	n := len(x.pm)
	if n == 0 {
		return 0, false, ErrEmpty
	}
	compares := 0
	lo, hi, ans := 0, n-1, int64(-1)
	for lo <= hi {
		mid := lo + (hi-lo)/2
		compares++
		if x.pm[mid] <= T {
			ans = int64(mid)
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	x.lastCompares.Store(int64(compares))
	if ans < 0 {
		return -1, false, nil
	}
	return ans, true, nil
}
