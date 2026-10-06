package maintenance

import "strconv"

// releaseLocked 解除工单与承包商的在手关系。
func (s *Service) releaseLocked(o *order) {
	if o.assignedTo == 0 {
		return
	}
	if c, ok := s.contractors[o.assignedTo]; ok {
		delete(c.holds, o.id)
		delete(c.pending, o.id)
	}
	o.assignedTo = 0
	o.dispatchedAt = 0
	o.responseDue = 0
	o.completeDue = 0
}

// assignLocked 把工单派给承包商并按当前等级重新起算两个时限。
func (s *Service) assignLocked(o *order, c *contractor, now int) {
	o.status = StatusDispatched
	o.assignedTo = c.id
	o.dispatchedAt = now
	o.responseDue = now + s.cfg.responseLimit(o.level)
	o.completeDue = now + s.cfg.completeLimit(o.level)
	c.holds[o.id] = struct{}{}
	c.pending[o.id] = struct{}{}
	s.emitLocked(now, EventDispatched, o.id, c.id, 0, "")
}

// afterRejectionLocked 处理累计拒单升级；紧急单不再升级。
// 升级在第 R、2R、… 次拒单时发生，等级不超过紧急。
func (s *Service) afterRejectionLocked(o *order, now int) {
	r := s.cfg.RejectUpgrade
	if o.level < LevelEmergency && r > 0 && o.rejectCount%r == 0 {
		o.level++
		s.emitLocked(now, EventUpgraded, o.id, 0, o.level,
			"rejections="+strconv.Itoa(o.rejectCount))
	}
}

// returnRejectedLocked 把被拒工单退回队列并登记拒绝者。
func (s *Service) returnRejectedLocked(o *order, now int, byContractor int, reason string) {
	s.releaseLocked(o)
	o.status = StatusQueued
	o.rejectCount++
	if byContractor != 0 {
		o.rejectedBy[byContractor] = struct{}{}
	}
	s.emitLocked(now, EventRejected, o.id, byContractor, 0, reason)
	s.afterRejectionLocked(o, now)
	s.queue.push(o)
}

// DispatchNext 按队列次序尝试派单；队首无候选则跳过，无任何可派单时返回
// ErrNoCandidate。紧急单无普通候选时允许抢占。
func (s *Service) DispatchNext(now int) (orderID, contractorID int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return 0, 0, err
	}
	s.settleLocked(now)

	var deferred []*order
	for s.queue.len() > 0 {
		o := s.queue.pop()
		if o.status != StatusQueued {
			continue // 已被撤销等操作失效的队列残留
		}
		c := s.selectContractor(o)
		if c == nil && o.level == LevelEmergency {
			if pc := s.selectPreemptor(o); pc != nil {
				victim := preemptVictim(pc, s.orders)
				s.releaseLocked(victim)
				victim.status = StatusQueued
				s.emitLocked(now, EventPreempted, victim.id, pc.id, 0,
					"for-order="+strconv.Itoa(o.id))
				s.queue.push(victim)
				c = pc
			}
		}
		if c == nil {
			deferred = append(deferred, o) // 无候选，留在队列，不阻塞后续
			continue
		}
		s.assignLocked(o, c, now)
		for _, d := range deferred {
			s.queue.push(d)
		}
		return o.id, c.id, nil
	}
	for _, d := range deferred {
		s.queue.push(d)
	}
	return 0, 0, ErrNoCandidate
}

// settleLocked 以 now 结算全部响应逾期与完成逾期，并处理伴随事件。
func (s *Service) settleLocked(now int) {
	for _, oid := range s.orderIDs {
		o := s.orders[oid]
		switch o.status {
		case StatusDispatched:
			// 响应时限“恰等”仍可确认，超过一个单位才视为拒单。
			if now > o.responseDue {
				s.returnRejectedLocked(o, now, o.assignedTo, "response_timeout")
			}
		case StatusConfirmed:
			if now > o.completeDue && !o.overdueEmitted {
				o.status = StatusOverdue
				o.overdueEmitted = true
				s.emitLocked(now, EventOverdue, o.id, o.assignedTo, 0,
					"completion_due="+strconv.Itoa(o.completeDue))
			}
		}
	}
}

// enqueueLocked 把工单放回队列并处理累计拒单升级。
func (s *Service) enqueueLocked(o *order) {
	s.queue.push(o)
}
