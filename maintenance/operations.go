package maintenance

func nonempty(xs []string) bool {
	if len(xs) == 0 {
		return false
	}
	for _, x := range xs {
		if x == "" {
			return false
		}
	}
	return true
}

func (s *Service) RegisterContractor(now int, trades, buildings []string, capacity int, acceptsEmergency bool) (int, error) {
	if !nonempty(trades) || !nonempty(buildings) || capacity <= 0 {
		return 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return 0, err
	}
	c := &contractor{
		id:               s.nextContractor + 1,
		trades:           map[string]struct{}{},
		buildings:        map[string]struct{}{},
		capacity:         capacity,
		acceptsEmergency: acceptsEmergency,
		active:           true,
		holds:            map[int]struct{}{},
		pending:          map[int]struct{}{},
	}
	for _, t := range trades {
		c.trades[t] = struct{}{}
	}
	for _, b := range buildings {
		c.buildings[b] = struct{}{}
	}
	s.nextContractor++
	s.contractors[c.id] = c
	s.contractorIDs = append(s.contractorIDs, c.id)
	return c.id, nil
}

func (s *Service) SubmitOrder(now int, tenant, trade, building string, level Level) (int, error) {
	if tenant == "" || trade == "" || building == "" || !level.valid() {
		return 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return 0, err
	}
	s.settleLocked(now)
	o := &order{
		id:          s.nextOrderID + 1,
		tenant:      tenant,
		trade:       trade,
		building:    building,
		level:       level,
		submittedAt: now,
		status:      StatusQueued,
		rejectedBy:  map[int]struct{}{},
	}
	s.nextOrderID++
	s.orders[o.id] = o
	s.orderIDs = append(s.orderIDs, o.id)
	s.queue.push(o)
	return o.id, nil
}

func (s *Service) Confirm(now, orderID, contractorID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return err
	}
	s.settleLocked(now)
	o, ok := s.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := s.contractors[contractorID]; !ok {
		return ErrNotFound
	}
	if o.status != StatusDispatched {
		return ErrInvalidState // 已确认/已完成/已撤销/队列中均不可确认
	}
	if o.assignedTo != contractorID {
		return ErrForbidden // 非承接者确认
	}
	o.status = StatusConfirmed
	o.confirmedAt = now
	delete(s.contractors[contractorID].pending, o.id)
	return nil
}

func (s *Service) Reject(now, orderID, contractorID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return err
	}
	s.settleLocked(now)
	o, ok := s.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := s.contractors[contractorID]; !ok {
		return ErrNotFound
	}
	if o.status != StatusDispatched {
		return ErrInvalidState
	}
	if o.assignedTo != contractorID {
		return ErrForbidden
	}
	s.returnRejectedLocked(o, now, contractorID, "manual_reject")
	return nil
}

func (s *Service) Complete(now, contractorID, orderID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return err
	}
	s.settleLocked(now)
	o, ok := s.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := s.contractors[contractorID]; !ok {
		return ErrNotFound
	}
	if o.status != StatusConfirmed && o.status != StatusOverdue {
		return ErrInvalidState // 已完成再完成、未确认即完成均拒绝
	}
	if o.assignedTo != contractorID {
		return ErrForbidden
	}
	s.releaseLocked(o)
	o.status = StatusCompleted
	c := s.contractors[contractorID]
	c.completedBefore = true
	c.lastCompletion = now
	return nil
}

func (s *Service) Cancel(now int, tenant string, orderID int) error {
	if tenant == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return err
	}
	s.settleLocked(now)
	o, ok := s.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	if o.status != StatusQueued && o.status != StatusDispatched {
		return ErrInvalidState // 确认后不可撤销，已完成/已撤销也不允许
	}
	if o.tenant != tenant {
		return ErrForbidden // 非提交者撤销
	}
	if o.status == StatusDispatched {
		s.releaseLocked(o)
	}
	o.status = StatusCancelled
	return nil
}

func (s *Service) Deactivate(now, contractorID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return err
	}
	s.settleLocked(now)
	c, ok := s.contractors[contractorID]
	if !ok {
		return ErrNotFound
	}
	if !c.active {
		return ErrInvalidState // 已停用再派/再停用
	}
	c.active = false
	// 在手未确认工单全部回队（拒单次数不增加，不锁死该承包商以外的可能性）。
	for oid := range c.holds {
		o := s.orders[oid]
		if o.status == StatusDispatched {
			s.releaseLocked(o)
			o.status = StatusQueued
			s.emitLocked(now, EventReturned, o.id, c.id, 0, "contractor_deactivated")
			s.queue.push(o)
		}
	}
	return nil
}

// Tick 仅推进时钟并结算逾期，不派单。
func (s *Service) Tick(now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceLocked(now); err != nil {
		return err
	}
	s.settleLocked(now)
	return nil
}

func (s *Service) GetOrder(now, orderID int) (OrderView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.hasNow && now < s.lastNow {
		return OrderView{}, ErrClockBackward
	}
	o, ok := s.orders[orderID]
	if !ok {
		return OrderView{}, ErrNotFound
	}
	v := OrderView{
		ID:            o.id,
		Tenant:        o.tenant,
		Trade:         o.trade,
		Building:      o.building,
		Level:         o.level,
		SubmittedAt:   o.submittedAt,
		Status:        o.status,
		AssignedTo:    o.assignedTo,
		DispatchedAt:  o.dispatchedAt,
		ConfirmedAt:   o.confirmedAt,
		ResponseDue:   o.responseDue,
		CompletionDue: o.completeDue,
		RejectCount:   o.rejectCount,
	}
	// 查询反映 now 下应有状态，但不改变系统（不产生事件）。
	if v.Status == StatusDispatched && now > o.responseDue {
		v.Status = StatusQueued
		v.AssignedTo = 0
	} else if v.Status == StatusConfirmed && now > o.completeDue {
		v.Status = StatusOverdue
	}
	return v, nil
}

func (s *Service) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}
