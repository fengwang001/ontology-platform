package slots

// Return 返还系列中某一周，立即释放该周容量并触发等候名单分配。一次返还只释放这一周。
func (e *Engine) Return(seriesID, week int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if seriesID < 0 || week < 0 {
		return reject(ReasonInvalidParam, "系列或周编号非法")
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	s, ok := e.series[seriesID]
	if !ok {
		return reject(ReasonNotFound, "系列 %d 不存在", seriesID)
	}
	if e.phase == phaseSettled {
		return reject(ReasonSeasonSettled, "航季已结算")
	}
	if week < s.StartWeek || week > s.EndWeek {
		return reject(ReasonInvalidParam, "周 %d 不在系列范围内", week)
	}
	if s.weekState(week) != WeekPlanned {
		return reject(ReasonInvalidParam, "周 %d 已返还/登记/豁免", week)
	}
	if now < e.cfg.ReturnDeadline {
		s.Weeks[week] = WeekReturnedEarly
	} else {
		s.Weeks[week] = WeekReturnedLate
	}
	slot := SlotKey{Weekday: s.Weekday, Hour: s.Hour}
	e.occ[CellKey{Week: week, Weekday: s.Weekday, Hour: s.Hour}]--
	e.slotTree(slot).add(week, -1)
	e.promote(slot, week)
	e.advance(now)
	return nil
}

// Swap 两家公司交换两个同周数范围的系列，交换后历史使用率记在接收方名下。
func (e *Engine) Swap(aID, bID int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if aID < 0 || bID < 0 || aID == bID {
		return reject(ReasonInvalidParam, "系列编号非法")
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	a, ok := e.series[aID]
	if !ok {
		return reject(ReasonNotFound, "系列 %d 不存在", aID)
	}
	b, ok := e.series[bID]
	if !ok {
		return reject(ReasonNotFound, "系列 %d 不存在", bID)
	}
	if e.phase == phaseSettled {
		return reject(ReasonSeasonSettled, "航季已结算")
	}
	if a.Airline == b.Airline {
		return reject(ReasonInvalidParam, "两个系列属于同一家公司")
	}
	if a.StartWeek != b.StartWeek || a.EndWeek != b.EndWeek {
		return reject(ReasonInvalidParam, "两个系列周数范围不同")
	}
	if hasExecuted(a) || hasExecuted(b) {
		return reject(ReasonSwapExecuted, "系列所在周已执行，不可交换")
	}
	a.Airline, b.Airline = b.Airline, a.Airline
	e.advance(now)
	return nil
}

func hasExecuted(s *Series) bool {
	for _, st := range s.Weeks {
		if st == WeekExecuted {
			return true
		}
	}
	return false
}

// Register 在某周结束后的登记窗口内（窗口终点取闭）登记该周已执行或未执行。
func (e *Engine) Register(seriesID, week int, executed bool, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if seriesID < 0 || week < 0 {
		return reject(ReasonInvalidParam, "系列或周编号非法")
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	s, ok := e.series[seriesID]
	if !ok {
		return reject(ReasonNotFound, "系列 %d 不存在", seriesID)
	}
	if e.phase == phaseSettled {
		return reject(ReasonSeasonSettled, "航季已结算")
	}
	if week < s.StartWeek || week > s.EndWeek {
		return reject(ReasonInvalidParam, "周 %d 不在系列范围内", week)
	}
	st := s.weekState(week)
	if st == WeekReturnedEarly || st == WeekReturnedLate || st == WeekExempted {
		return reject(ReasonInvalidParam, "周 %d 已返还或豁免", week)
	}
	weekEnd := e.cfg.weekEnd(week)
	if now <= weekEnd {
		return reject(ReasonInvalidParam, "周 %d 尚未结束", week)
	}
	if now > weekEnd+e.cfg.RegistrationWindow {
		return reject(ReasonRegisterLate, "周 %d 登记超期", week)
	}
	if st != WeekPlanned {
		return reject(ReasonDuplicateRegister, "周 %d 重复登记", week)
	}
	if executed {
		s.Weeks[week] = WeekExecuted
	} else {
		s.Weeks[week] = WeekUnexecuted
	}
	e.advance(now)
	return nil
}

// Exempt 因不可抗力登记某周为豁免，该周既不计入分子也不计入分母。
func (e *Engine) Exempt(seriesID, week int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if seriesID < 0 || week < 0 {
		return reject(ReasonInvalidParam, "系列或周编号非法")
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	s, ok := e.series[seriesID]
	if !ok {
		return reject(ReasonNotFound, "系列 %d 不存在", seriesID)
	}
	if e.phase == phaseSettled {
		return reject(ReasonSeasonSettled, "航季已结算")
	}
	if week < s.StartWeek || week > s.EndWeek {
		return reject(ReasonInvalidParam, "周 %d 不在系列范围内", week)
	}
	if s.weekState(week) != WeekPlanned {
		return reject(ReasonInvalidParam, "周 %d 已返还/登记/豁免", week)
	}
	s.Weeks[week] = WeekExempted
	e.advance(now)
	return nil
}

// Grant 航季内直接分配一个系列（占用容量，满员报容量不足）。
func (e *Engine) Grant(spec RequestSpec, now int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.validateSpec(spec); err != nil {
		return -1, err
	}
	if err := e.checkClock(now); err != nil {
		return -1, err
	}
	if e.phase == phaseSettled {
		return -1, reject(ReasonSeasonSettled, "航季已结算")
	}
	if e.phase == phaseRequests {
		return -1, reject(ReasonPhaseMismatch, "尚未执行截止分配")
	}
	if !e.rangeFree(spec.Weekday, spec.Hour, spec.StartWeek, spec.EndWeek) {
		return -1, reject(ReasonCapacity, "小时段 %d/%d 容量不足", spec.Weekday, spec.Hour)
	}
	s := e.materialize(&Request{
		Airline: spec.Airline, Weekday: spec.Weekday, Hour: spec.Hour,
		StartWeek: spec.StartWeek, EndWeek: spec.EndWeek, SubmittedAt: now,
	})
	e.advance(now)
	return s.ID, nil
}
