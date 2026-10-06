package scheduler

import "sort"

// slot 时段名额账本条目，区间为左闭右开 [start, end)。
// occupied 为实时维护的已占名额计数，与预约总数无关。
type slot struct {
	start    int64
	end      int64
	cap      int
	occupied int
}

// hasRoom 是否有余量；超额（occupied > cap）时同样返回 false，
// 因此超额时段自然不参与新预约与顺延候选。
func (s *slot) hasRoom() bool { return s.occupied < s.cap }

// region 一个区域的时段集合，slots 按起点升序且互不重叠。
type region struct {
	id    string
	slots []*slot
}

func (r *region) slotIndex(start int64) int {
	return sort.Search(len(r.slots), func(i int) bool { return r.slots[i].start >= start })
}

func (r *region) findSlot(start int64) *slot {
	i := r.slotIndex(start)
	if i < len(r.slots) && r.slots[i].start == start {
		return r.slots[i]
	}
	return nil
}

// addSlot 校验参数与重叠后按起点有序插入。
func (r *region) addSlot(start, end int64, cap int) error {
	if end <= start || cap < 0 {
		return newError(CodeInvalidParam, "slot requires end > start and cap >= 0")
	}
	i := r.slotIndex(start)
	if i < len(r.slots) && r.slots[i].start == start {
		return newError(CodeInvalidParam, "duplicate slot start")
	}
	if i > 0 && r.slots[i-1].end > start {
		return newError(CodeInvalidParam, "slot overlaps predecessor")
	}
	if i < len(r.slots) && r.slots[i].start < end {
		return newError(CodeInvalidParam, "slot overlaps successor")
	}
	s := &slot{start: start, end: end, cap: cap}
	r.slots = append(r.slots, nil)
	copy(r.slots[i+1:], r.slots[i:])
	r.slots[i] = s
	return nil
}

// shiftCandidate 在起点位于 (origStart, origStart+maxSpan] 且不晚于
// latestStart 的时段中，找最早一个有余量的。开销为 O(跨度内时段数)，
// 与区域内预约总数无关。
func (r *region) shiftCandidate(origStart, maxSpan, latestStart int64) *slot {
	i := sort.Search(len(r.slots), func(i int) bool { return r.slots[i].start > origStart })
	for ; i < len(r.slots); i++ {
		s := r.slots[i]
		if s.start-origStart > maxSpan || s.start > latestStart {
			break
		}
		if s.hasRoom() {
			return s
		}
	}
	return nil
}

// listSlots 返回与 [from, to) 相交的时段视图。
func (r *region) listSlots(from, to int64) []SlotView {
	var out []SlotView
	for _, s := range r.slots {
		if s.end > from && s.start < to {
			out = append(out, SlotView{
				Start:    s.start,
				End:      s.end,
				Cap:      s.cap,
				Occupied: s.occupied,
				Oversold: s.occupied > s.cap,
			})
		}
	}
	return out
}
