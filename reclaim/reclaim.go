// Package reclaim 维护回收水位与每键候选堆，实现不全表扫描的增量回收。
package reclaim

import (
	"container/heap"

	"ontology/txid"
)

// ChainView 是回收器对单键版本链所需的最小视图（由 store 实现）。
type ChainView interface {
	BlockCT() (txid.TXID, bool)
	Advance(horizon txid.TXID) (inspected, reclaimed int)
}

// ChainLister 按键名取链；仅在候选堆命中该键时调用。
type ChainLister interface {
	ChainFor(key string) (ChainView, bool)
}

type item struct {
	key string
	ct  txid.TXID
	idx int
}

type candHeap []*item

func (h candHeap) Len() int            { return len(h) }
func (h candHeap) Less(i, j int) bool  { return h[i].ct.Before(h[j].ct) }
func (h candHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i]; h[i].idx, h[j].idx = i, j }
func (h *candHeap) Push(x any)         { it := x.(*item); it.idx = len(*h); *h = append(*h, it) }
func (h *candHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// Reclaimer 持有水位、候选堆与非导出的考察/回收计数。
type Reclaimer struct {
	watermark txid.TXID
	heap      candHeap
	queued    map[string]*item

	inspected int
	reclaimed int
}

// New 创建空回收器。
func New() *Reclaimer {
	return &Reclaimer{heap: candHeap{}, queued: map[string]*item{}}
}

// Watermark 返回当前回收水位（只升不降）。
func (r *Reclaimer) Watermark() txid.TXID { return r.watermark }

// Inspected 返回历次 Advance 实际考察过的版本总数。
func (r *Reclaimer) Inspected() int { return r.inspected }

// Reclaimed 返回已回收版本总数。
func (r *Reclaimer) Reclaimed() int { return r.reclaimed }

// NoteCommit 在某键产生新版本后登记/刷新该键候选；每键在堆中至多一项。
func (r *Reclaimer) NoteCommit(key string, view ChainView) {
	if it, ok := r.queued[key]; ok {
		if ct, has := view.BlockCT(); has && ct != it.ct {
			it.ct = ct
			heap.Fix(&r.heap, it.idx)
		}
		return
	}
	ct, has := view.BlockCT()
	if !has {
		return
	}
	it := &item{key: key, ct: ct}
	heap.Push(&r.heap, it)
	r.queued[key] = it
}

// forget 把一个键移出候选堆；被移出的键之后不再入堆（其链已无遮蔽可能）。
func (r *Reclaimer) forget(it *item) {
	heap.Remove(&r.heap, it.idx)
	delete(r.queued, it.key)
}

// Advance 以 horizon 推进水位并增量回收。调用方须保证此后不会出现
// point 小于等于 watermark 的新快照（store 在同一临界区内保证）。
func (r *Reclaimer) Advance(horizon txid.TXID, lister ChainLister) {
	if horizon.Before(r.watermark) || horizon == r.watermark {
		// 水位不回退；相等也无新版本可越过边界。
	} else {
		r.watermark = horizon
	}
	for r.heap.Len() > 0 {
		top := r.heap[0]
		if !top.ct.Before(horizon) {
			return // 堆按最小 ct 排序，其余键候选都越不过边界。
		}
		view, ok := lister.ChainFor(top.key)
		if !ok {
			r.forget(top)
			continue
		}
		inspected, reclaimed := view.Advance(horizon)
		r.inspected += inspected
		r.reclaimed += reclaimed
		ct, has := view.BlockCT()
		switch {
		case !has:
			r.forget(top) // 链长 <2，该键永久无可回收版本。
		case reclaimed == 0:
			// 该键当前越不过边界：刷新成实际遮蔽号并下沉，继续处理其余键。
			top.ct = ct
			heap.Fix(&r.heap, top.idx)
		default:
			top.ct = ct
			heap.Fix(&r.heap, top.idx)
		}
	}
}
