package booking

// slot 是单个区域内一个左闭右开时段的名额账本。
type slot struct {
	start    int64
	end      int64
	capacity int
	occupied int
}

type region struct {
	name  string
	slots []*slot // 按 start 升序、互不重叠
}

// findSlot 在有序时段数组中定位起点恰为 start 的时段；返回 (slot, found)。
// 纯二分，O(log S)，与该区域预约总数无关。
func (r *region) findSlot(start int64) (*slot, bool) {
	lo, hi := 0, len(r.slots)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch {
		case r.slots[mid].start == start:
			return r.slots[mid], true
		case r.slots[mid].start < start:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return nil, false
}

// insertSlot 插入新时段，拒绝半开区间重叠。
func (r *region) insertSlot(s *slot) error {
	lo, hi := 0, len(r.slots)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if r.slots[mid].start < s.start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if _, ok := r.findSlot(s.start); ok {
		return bookingError(ErrInvalidParam, "slot start duplicated")
	}
	if lo > 0 && r.slots[lo-1].end > s.start {
		return bookingError(ErrInvalidParam, "slot overlaps previous slot")
	}
	if lo < len(r.slots) && s.end > r.slots[lo].start {
		return bookingError(ErrInvalidParam, "slot overlaps next slot")
	}
	r.slots = append(r.slots, nil)
	copy(r.slots[lo+1:], r.slots[lo:])
	r.slots[lo] = s
	return nil
}

// hasRoom 判定时段是否可接受新预约：超额（occupied>capacity）与满
// （occupied==capacity）均无余量；超额时段因此既不能直接落位，
// 也不会出现在顺延候选中。
func (s *slot) hasRoom() bool { return s.occupied < s.capacity }

func (s *slot) overbooked() bool { return s.occupied > s.capacity }

func (s *slot) info() SlotInfo {
	return SlotInfo{
		Start:      s.start,
		End:        s.end,
		Capacity:   s.capacity,
		Occupied:   s.occupied,
		Overbooked: s.overbooked(),
	}
}

// SlotInfo 为时段名额查询结果。
type SlotInfo struct {
	Start      int64
	End        int64
	Capacity   int
	Occupied   int
	Overbooked bool
}
