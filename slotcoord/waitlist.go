package slotcoord

import "sort"

// waitlistAdd 把未被满足的申请加入其小时段的等候名单。
// 不变式: 名单中的申请至少有一个覆盖单元格剩余容量为 0(blocked >= 1)。
func (s *System) waitlistAdd(req Request) {
	blocked := 0
	for _, c := range req.cells() {
		if s.remainingOf(c) <= 0 {
			blocked++
		}
		if s.cellWaiters[c] == nil {
			s.cellWaiters[c] = map[int]bool{}
		}
		s.cellWaiters[c][req.ID] = true
	}
	slot := req.slot()
	s.waitOrder[slot] = append(s.waitOrder[slot], req.ID)
	s.waitEntry[req.ID] = &waitEntry{req: req, blocked: blocked}
	s.log("申请 %d 进入小时段 %+v 等候名单, 阻塞单元格数=%d", req.ID, slot, blocked)
}

// waitlistRemove 把申请从等候名单与单元格索引中移除。
func (s *System) waitlistRemove(reqID int) {
	entry := s.waitEntry[reqID]
	if entry == nil {
		return
	}
	for _, c := range entry.req.cells() {
		delete(s.cellWaiters[c], reqID)
		if len(s.cellWaiters[c]) == 0 {
			delete(s.cellWaiters, c)
		}
	}
	slot := entry.req.slot()
	order := s.waitOrder[slot]
	for i, id := range order {
		if id == reqID {
			s.waitOrder[slot] = append(order[:i:i], order[i+1:]...)
			break
		}
	}
	if len(s.waitOrder[slot]) == 0 {
		delete(s.waitOrder, slot)
	}
	delete(s.waitEntry, reqID)
}

// releaseCell 释放一个单元格(一次返还只释放这一周), 并把容量按等候
// 名单次序分配给覆盖该周且仍可满足的等候申请。开销只随该单元格的
// 等候者数量与被满足的申请者自身周数增长, 与机场内系列总数和
// 航季总周数无关(由 Stats.WaitlistCellVisits 可验证)。
func (s *System) releaseCell(c Cell) {
	old := s.remainingOf(c)
	s.remaining[c] = old + 1
	if old != 0 {
		return // 该单元格此前并未耗尽, 不会解除任何等候申请的阻塞
	}
	waiters := s.cellWaiters[c]
	if len(waiters) == 0 {
		return
	}
	var ready []int
	for reqID := range waiters {
		s.stats.WaitlistCellVisits++
		entry := s.waitEntry[reqID]
		entry.blocked--
		if entry.blocked == 0 {
			ready = append(ready, reqID)
		}
	}
	// 申请 ID 单调递增即提交次序, 排序后按名单次序处理。
	sort.Ints(ready)
	for _, reqID := range ready {
		entry := s.waitEntry[reqID]
		if entry == nil || entry.blocked != 0 {
			continue // 已被先处理的申请者重新阻塞
		}
		s.waitlistRemove(reqID)
		s.log("等候申请 %d 因单元格 %+v 释放而被满足", reqID, c)
		s.allocateRequest(&entry.req, true)
	}
}
