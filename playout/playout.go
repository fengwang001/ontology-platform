// Package playout 实现任一时刻的播出判定与垫片偏移。
package playout

import (
	"sync/atomic"

	"ontology/slot"
)

// Kind 播出内容类型。
type Kind int8

const (
	Filler   Kind = iota // 垫片
	Program              // 常规节目
	Override             // 插播（抢占式或顺延式）
)

// Result 时刻 t 的播出内容与偏移。
type Result struct {
	Kind   Kind
	ID     string
	Offset int64
}

// probes 统计一次 At 查询比较的段数。
var probes atomic.Int64

// At 只读查询时刻 t 播出的内容与偏移。
// 优先次序：插播段 > 常规节目段 > 垫片。
func At(tl *slot.Timeline, t int64) Result {
	probes.Store(0)
	var res Result
	tl.View(func(st *slot.State) {
		res = at(st, t)
	})
	return res
}

func at(st *slot.State, t int64) Result {
	// 1. 抢占式插播段。
	if seg := lastLE(st.Preempt, t); seg != nil {
		probes.Add(1)
		if seg.End > t {
			return Result{Kind: Override, ID: seg.ID, Offset: t - seg.Start}
		}
	}
	// 2. 常规节目段 / 顺延式插播段。
	seg := lastLE(st.Layout, t)
	if seg != nil {
		probes.Add(1)
		if seg.End > t {
			if seg.Kind == slot.Shift {
				return Result{Kind: Override, ID: seg.ID, Offset: t - seg.Start}
			}
			return Result{Kind: Program, ID: seg.ID, Offset: seg.Offset + t - seg.Start}
		}
	}
	// 3. 垫片：空隙起点为 t 之前最近的常规节目段或顺延式插播段的终点，没有则为 0。
	gapStart := int64(0)
	if seg != nil {
		gapStart = seg.End
	}
	return Result{Kind: Filler, Offset: (t - gapStart) % st.Filler}
}

// lastLE 返回按起点有序、两两不相交的段集合中最后一个 Start <= t 的段。
func lastLE(segs []*slot.Segment, t int64) *slot.Segment {
	lo, hi := 0, len(segs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		probes.Add(1)
		if segs[mid].Start <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return nil
	}
	return segs[lo-1]
}
