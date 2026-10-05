// Package dvr 维护单条流的回看窗口：封片入窗后的淘汰与区间清单查询。
//
// 淘汰规则：只要窗口内总时长减去最旧片时长仍不小于 W，就淘汰最旧片，
// 重复直到不满足（淘汰后总时长恰等 W 允许）。窗口用 slice+head 作队列，
// 淘汰均摊 O(1)；Playlist 二分定位首片，比较次数不超过 2·ceil(log2(n+1))，
// 其后只顺序触碰被返回的片。
package dvr

import (
	"errors"

	"ontology/segment"
)

var (
	// ErrNotYetProduced 表示尚无任何已封片，或 from 不早于最新已封片终点。
	ErrNotYetProduced = errors.New("dvr: 尚未产生")
	// ErrSlidOut 表示 from 早于窗口内最旧片起点，区间已滑出窗口。
	ErrSlidOut = errors.New("dvr: 已滑出窗口")
)

// List 是一次 Playlist 查询的结果。
type List struct {
	Segments              []segment.Segment // 与区间相交的片，按 seq 升序
	MediaSequence         int64             // 首个返回片的 seq
	DiscontinuitySequence int64             // seq 小于首片的带标记片总数（含已淘汰）
}

// Window 是单条流的回看窗口。
type Window struct {
	w      int64             // 回看窗口时长
	segs   []segment.Segment // 逻辑队列为 segs[head:]
	head   int               // 队首下标，淘汰只推进不搬移
	total  int64             // 窗口内总时长
	probes int               // 最近一次 Playlist 定位首片的比较次数（供测试）
}

// NewWindow 构造窗口时长为 w 的回看窗口。
func NewWindow(w int64) *Window { return &Window{w: w} }

// Len 返回窗口内片数。
func (w *Window) Len() int { return len(w.segs) - w.head }

// Add 封片入窗并按规则淘汰最旧片。
func (w *Window) Add(seg segment.Segment) {
	w.segs = append(w.segs, seg)
	w.total += seg.Dur
	for w.total-w.segs[w.head].Dur >= w.w {
		w.total -= w.segs[w.head].Dur
		w.head++
	}
	// head 过半且足够大时整体压缩，保证均摊 O(1) 与内存有界。
	if w.head > 64 && w.head*2 >= len(w.segs) {
		w.segs = append([]segment.Segment(nil), w.segs[w.head:]...)
		w.head = 0
	}
}

// Playlist 取媒体时间半开区间 [from, to) 与窗口相交的片。
// 调用方须保证 0 <= from < to；to 大于最新已封片终点时截到该终点。
func (w *Window) Playlist(from, to int64) (List, error) {
	n := w.Len()
	if n == 0 {
		return List{}, ErrNotYetProduced
	}
	segs := w.segs[w.head:]
	lo := segs[0].Start
	hi := segs[n-1].End()
	if from >= hi {
		return List{}, ErrNotYetProduced
	}
	if from < lo {
		return List{}, ErrSlidOut
	}
	if to > hi {
		to = hi
	}
	// 二分定位：最后一个 Start <= from 的片（片时间首尾相接，必相交）。
	w.probes = 0
	loIdx, hiIdx := 0, n-1
	for loIdx < hiIdx {
		mid := (loIdx + hiIdx + 1) / 2
		w.probes++
		if segs[mid].Start <= from {
			loIdx = mid
		} else {
			hiIdx = mid - 1
		}
	}
	out := List{}
	for i := loIdx; i < n && segs[i].Start < to; i++ {
		out.Segments = append(out.Segments, segs[i])
	}
	out.MediaSequence = out.Segments[0].Seq
	out.DiscontinuitySequence = out.Segments[0].DiscBefore
	return out, nil
}
