package slotcoord

// CloseApplications 在申请截止时刻执行一次性结算分配：
//  1. 全部历史优先权申请(相互之间超容量属参数非法, 整个操作被拒绝);
//  2. 每个单元格把此时剩余容量的一半(向下取整)记为新进入者保留额,
//     新进入者申请按提交时刻早者先满足, 每满足一个其覆盖单元格保留额各减一;
//  3. 其余申请按提交时刻早者先处理, 要求每个覆盖单元格剩余容量减去保留额仍大于零。
//
// 未被满足的申请进入该小时段的等候名单。分配按单元格计, 全有或全无。
func (s *System) CloseApplications(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var paramErr, phaseErr error
	switch {
	case s.settled:
		phaseErr = ErrSeasonSettled
	case s.closed:
		paramErr = ErrParam // 重复结算申请
	case now < s.cfg.ApplicationDeadline:
		paramErr = ErrParam // 尚未到申请截止时刻
	}
	histErr := s.historicFeasibilityErr()
	if err := firstError(paramErr, histErr, s.clockErr(now), phaseErr); err != nil {
		return err
	}
	s.accept(now)
	s.closed = true

	requests := s.requests
	allocated := make([]bool, len(requests))
	held := map[string]int{}

	// 第一轮: 历史优先权申请, 可行性已校验, 全部满足。
	for i := range requests {
		if !s.isHistoric(requests[i]) {
			continue
		}
		s.allocateRequest(&requests[i], false)
		allocated[i] = true
		held[requests[i].Airline]++
		s.log("申请 %d 以历史优先权被满足", requests[i].ID)
	}

	// 新进入者保留额快照: 每个相关单元格剩余容量的一半(向下取整)。
	s.reserve = map[Cell]int{}
	for i := range requests {
		if allocated[i] {
			continue
		}
		for _, c := range requests[i].cells() {
			if _, ok := s.reserve[c]; !ok {
				s.reserve[c] = s.remainingOf(c) / 2
			}
		}
	}

	// 第二轮: 新进入者申请, 按提交次序; 判定那一刻持有系列数少于阈值即为新进入者。
	for i := range requests {
		if allocated[i] {
			continue
		}
		req := &requests[i]
		if held[req.Airline] >= s.cfg.NewcomerThreshold {
			continue
		}
		ok := true
		for _, c := range req.cells() {
			if s.reserve[c] <= 0 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for _, c := range req.cells() {
			s.reserve[c]--
			s.remaining[c] = s.remainingOf(c) - 1
		}
		s.createSeries(req)
		allocated[i] = true
		held[req.Airline]++
		s.log("申请 %d 以新进入者保留额被满足", req.ID)
	}

	// 第三轮: 其余申请, 按提交次序; 要求每个单元格剩余容量减去保留额仍大于零。
	for i := range requests {
		if allocated[i] {
			continue
		}
		req := &requests[i]
		ok := true
		for _, c := range req.cells() {
			if s.remainingOf(c)-s.reserve[c] <= 0 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		s.allocateRequest(req, false)
		allocated[i] = true
		held[req.Airline]++
		s.log("申请 %d 在普通轮次被满足", req.ID)
	}
	s.reserve = nil

	// 未被满足的申请进入该小时段的等候名单, 按提交时刻排序。
	for i := range requests {
		if !allocated[i] {
			s.waitlistAdd(requests[i])
		}
	}
	return nil
}

// historicFeasibilityErr 校验历史申请合计需求不超过任何单元格容量。
func (s *System) historicFeasibilityErr() error {
	if s.closed || s.settled {
		return nil
	}
	demand := map[Cell]int{}
	for i := range s.requests {
		if !s.isHistoric(s.requests[i]) {
			continue
		}
		for _, c := range s.requests[i].cells() {
			demand[c]++
			if demand[c] > s.cfg.Capacity {
				s.log("历史申请合计需求超容量: 单元格=%+v 需求=%d 容量=%d", c, demand[c], s.cfg.Capacity)
				return ErrParam
			}
		}
	}
	return nil
}

// createSeries 把申请转为系列(不触碰容量计数)。
func (s *System) createSeries(req *Request) *Series {
	ser := &Series{
		ID:        s.nextID,
		Airline:   req.Airline,
		Holder:    req.Airline,
		Weekday:   req.Weekday,
		Hour:      req.Hour,
		StartWeek: req.StartWeek,
		EndWeek:   req.EndWeek,
		weeks:     make([]weekState, req.weekCount()),
	}
	s.nextID++
	s.series[ser.ID] = ser
	return ser
}

// allocateRequest 把申请转为系列并扣减其覆盖单元格的剩余容量。
// bumpWaiters 为 true 时(航季内等候名单分配), 单元格耗尽会同步
// 增加覆盖它的等候申请的阻塞计数。
func (s *System) allocateRequest(req *Request, bumpWaiters bool) *Series {
	ser := s.createSeries(req)
	for _, c := range req.cells() {
		s.consumeCell(c, bumpWaiters)
	}
	return ser
}

// consumeCell 扣减一个单元格的剩余容量。
func (s *System) consumeCell(c Cell, bumpWaiters bool) {
	rem := s.remainingOf(c) - 1
	s.remaining[c] = rem
	if rem == 0 && bumpWaiters {
		for waiterID := range s.cellWaiters[c] {
			s.stats.WaitlistCellVisits++
			s.waitEntry[waiterID].blocked++
		}
	}
}
