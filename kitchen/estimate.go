package kitchen

import (
	"container/heap"
	"sort"
)

// order 是内部订单实体（types.go 中的 OrderInfo 为对外快照）。
type order struct {
	info        OrderInfo
	kind        OrderKind
	duration    int64
	targetStart int64 // 预约单：TargetPickup-Duration
	admittedAt  int64 // 即时单的最早可开工时刻（接单时刻）
	projected   int64 // 制作中：按声明时长的推定完成时刻（开工+时长）
}

// allocItem 是一次推定中的可排产对象。
type allocItem struct {
	o        *order
	ready    int64 // 可开工时刻：即时单=接单时刻，预约单=目标开工时刻
	seq      int64
	res      bool
	duration int64 // 仅无实体的假想候选单使用
}

// slotHeap 是制作位变空时刻的最小堆。
type slotHeap []int64

func (h slotHeap) Len() int           { return len(h) }
func (h slotHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h slotHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *slotHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *slotHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// allocateSlots 是核心排产模拟器：slotFree 显式给出每个制作位的“变空时刻”，
// 长度必须等于并行上限；在制位取其 projected，空闲位取真实空出锚点。
//
// 规则：
//   - 每个制作位同一时刻只做一笔；
//   - 每次取最早变空的制作位，在“最早可开工的预约单”与“最早接单的即时单”
//     之间选择能更早开工者；开工时刻相同则预约单插队优先；
//   - 没有即时单时，制作位可空闲等待未来到达目标开工时刻的预约单；
//   - 目标开工时刻晚于某单自身推定开工时刻的预约单不会被排到该单之前。
//
// 复杂度 O((P+W+E) log P)，P=并行上限，W=等待单数，E=假想单数；
// 不触碰已完成订单，故开销不随历史已完成订单数增长。
func allocateSlots(parallelism int, slotFree []int64, waiting []*order, extras ...allocItem) map[*order]int64 {
	if len(slotFree) != parallelism {
		panic("kitchen: slotFree length must equal parallelism")
	}
	slots := make(slotHeap, parallelism)
	copy(slots, slotFree)
	heap.Init(&slots)

	resv := make([]allocItem, 0, len(waiting)+len(extras))
	imm := make([]allocItem, 0, len(waiting)+len(extras))
	for _, wo := range waiting {
		it := itemOf(wo)
		if it.res {
			resv = append(resv, it)
		} else {
			imm = append(imm, it)
		}
	}
	for _, ex := range extras {
		if ex.res {
			resv = append(resv, ex)
		} else {
			imm = append(imm, ex)
		}
	}
	sort.Slice(resv, func(i, j int) bool {
		if resv[i].ready != resv[j].ready {
			return resv[i].ready < resv[j].ready
		}
		return resv[i].seq < resv[j].seq
	})
	sort.Slice(imm, func(i, j int) bool { return imm[i].seq < imm[j].seq })

	start := make(map[*order]int64, len(resv)+len(imm))
	ri, ii := 0, 0
	for ri < len(resv) || ii < len(imm) {
		freeAt := heap.Pop(&slots).(int64)
		var chosen allocItem
		switch {
		case ri < len(resv) && ii < len(imm):
			rs := max64(freeAt, resv[ri].ready)
			is := max64(freeAt, imm[ii].ready)
			if rs <= is { // 同时刻预约单插队优先
				chosen = resv[ri]
				ri++
			} else {
				chosen = imm[ii]
				ii++
			}
		case ri < len(resv):
			chosen = resv[ri]
			ri++
		default:
			chosen = imm[ii]
			ii++
		}
		s := max64(freeAt, chosen.ready)
		if chosen.o != nil {
			start[chosen.o] = s
		}
		heap.Push(&slots, s+itemDuration(chosen))
	}
	return start
}

// itemOf 把内部订单映射为排产对象。
func itemOf(o *order) allocItem {
	if o.kind == Reservation {
		return allocItem{o: o, ready: o.targetStart, seq: o.info.EnqueueSeq, res: true}
	}
	return allocItem{o: o, ready: o.admittedAt, seq: o.info.EnqueueSeq}
}

func itemDuration(it allocItem) int64 {
	if it.o != nil {
		return it.o.duration
	}
	return it.duration
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
