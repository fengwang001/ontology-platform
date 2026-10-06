package kitchen

import (
	"container/heap"
	"sort"
)

// waitOrder 是开工推定中一笔等待开工订单的视图。
type waitOrder struct {
	duration int64
	release  int64 // 可开工时刻：预约单为目标开工时刻，即时单为接单时刻
	resv     bool
	seq      int64 // 接单序号，保证推定次序确定
}

type slotEntry struct {
	freeAt int64
	idx    int
}

// slotHeap 按 (空出时刻, 制作位编号) 排序，保证推定结果确定。
type slotHeap []slotEntry

func (h slotHeap) Len() int { return len(h) }
func (h slotHeap) Less(i, j int) bool {
	if h[i].freeAt != h[j].freeAt {
		return h[i].freeAt < h[j].freeAt
	}
	return h[i].idx < h[j].idx
}
func (h slotHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *slotHeap) Push(x any)   { *h = append(*h, x.(slotEntry)) }
func (h *slotHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// estimateStarts 推定 waiting 中每笔订单的开工时刻，返回与 waiting 等长的切片。
//
// now 为当前时刻；busyUntil 给出每个制作位的预计空出时刻：
// 制作中的制作位为推定完成时刻（不早于 now），空闲制作位为实际空出时刻（可早于 now）。
//
// 调度规则：制作位空出时，目标开工时刻不晚于 max(空出时刻, now) 的预约单
// 优先于一切尚未开工的即时单；即时单按接单次序；开工时刻取空出时刻与
// 订单可开工时刻的较晚者，且即时单开工不早于 now。只剩未来预约单时
// 制作位空转到最近的目标开工时刻。
//
// 调度循环次数恰为 len(waiting)，堆规模为制作位数，因此一次推定的开销
// 只与当前排队及制作中的订单数有关，与历史已完成订单数无关。
func estimateStarts(now int64, busyUntil []int64, waiting []waitOrder) []int64 {
	starts := make([]int64, len(waiting))
	resv := make([]int, 0, len(waiting))
	inst := make([]int, 0, len(waiting))
	for i, o := range waiting {
		if o.resv {
			resv = append(resv, i)
		} else {
			inst = append(inst, i)
		}
	}
	sort.Slice(resv, func(a, b int) bool {
		oa, ob := waiting[resv[a]], waiting[resv[b]]
		if oa.release != ob.release {
			return oa.release < ob.release
		}
		return oa.seq < ob.seq
	})
	// inst 由调用方按接单次序构造，无需再排序。
	h := make(slotHeap, 0, len(busyUntil))
	for i, freeAt := range busyUntil {
		h = append(h, slotEntry{freeAt: freeAt, idx: i})
	}
	heap.Init(&h)
	ri, ii := 0, 0
	for done := 0; done < len(waiting); {
		slot := heap.Pop(&h).(slotEntry)
		eff := slot.freeAt
		if eff < now {
			eff = now
		}
		switch {
		case ri < len(resv) && waiting[resv[ri]].release <= eff:
			o := waiting[resv[ri]]
			start := slot.freeAt
			if o.release > start {
				start = o.release
			}
			starts[resv[ri]] = start
			ri++
			heap.Push(&h, slotEntry{freeAt: start + o.duration, idx: slot.idx})
			done++
		case ii < len(inst):
			o := waiting[inst[ii]]
			starts[inst[ii]] = eff
			ii++
			heap.Push(&h, slotEntry{freeAt: eff + o.duration, idx: slot.idx})
			done++
		default:
			// 只剩目标开工时刻晚于 eff 的预约单：制作位空转。
			heap.Push(&h, slotEntry{freeAt: waiting[resv[ri]].release, idx: slot.idx})
		}
	}
	return starts
}
