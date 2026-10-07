package leave

import "sort"

// RequestLeave 提交请假申请，覆盖闭区间 [from, to]。
// 区间内非工作日不扣额度；每个计扣日按所属年度分别扣减，
// 优先结转额度、其次当年额度。额度不足整单拒绝，不留任何痕迹。
// 申请一经提交即占用额度（计入已用）。返回假单 ID 与逐日扣减明细。
func (s *Service) RequestLeave(now int, empID string, from, to int) (int64, []Charge, error) {
	if now < 0 {
		return 0, nil, errf(CatInvalidParam, "now=%d is negative", now)
	}
	if empID == "" {
		return 0, nil, errf(CatInvalidParam, "empty employee id")
	}
	if from < 0 || to < from {
		return 0, nil, errf(CatInvalidParam, "invalid range [%d,%d]", from, to)
	}
	if from < now {
		return 0, nil, errf(CatInvalidParam, "from=%d is before now=%d", from, now)
	}
	if to-from > maxSpanDays {
		return 0, nil, errf(CatInvalidParam, "range [%d,%d] exceeds max span", from, to)
	}
	if err := s.checkClock(now); err != nil {
		return 0, nil, err
	}
	e := s.getEmployee(empID)
	if e == nil {
		return 0, nil, errf(CatEmployeeNotFound, "employee %q not found", empID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return 0, nil, err
	}
	if from < e.hireDay {
		return 0, nil, errf(CatInvalidParam, "from=%d is before hire day %d", from, e.hireDay)
	}
	if e.intervals.overlaps(from, to) {
		return 0, nil, errf(CatOverlap, "range [%d,%d] overlaps an existing leave", from, to)
	}

	nowYear := yearOf(now)
	// 模拟扣减：先算后写，失败不留痕。未来年度的结转额度尚未到
	// 年度切换，按 0 处理（见设计文档：结转仅由 now 触达时钉死）。
	type remain struct{ car, cur int }
	rems := make(map[int]*remain)
	getRem := func(y int) *remain {
		if r, ok := rems[y]; ok {
			return r
		}
		r := &remain{}
		if y <= nowYear {
			a := e.acctRO(y)
			var usedCar, pendCar int
			if a != nil {
				usedCar, pendCar = a.cur.usedCar, a.cur.pendCar
			}
			r.car = s.carryOf(e, y) - usedCar - pendCar
		}
		var usedCur, pendCur, carriedOut int
		if a := e.acctRO(y); a != nil {
			usedCur, pendCur, carriedOut = a.cur.usedCur, a.cur.pendCur, a.cur.carriedOut
		}
		r.cur = s.quota(e, y) - usedCur - pendCur - carriedOut
		rems[y] = r
		return r
	}

	var charges []chargeRec
	for d := from; d <= to; d++ {
		if s.nonWork[doyOf(d)] {
			continue
		}
		y := yearOf(d)
		r := getRem(y)
		switch {
		case doyOf(d) <= s.cfg.CarryDeadline && r.car > 0:
			r.car--
			charges = append(charges, chargeRec{day: d, carried: true})
		case r.cur > 0:
			r.cur--
			charges = append(charges, chargeRec{day: d})
		default:
			return 0, nil, errf(CatInsufficientQuota,
				"day %d (year %d): no available quota", d, y)
		}
	}

	// 模拟通过，正式生效：钉死结转、占用额度、登记假单。
	s.pinCarries(e, nowYear, now)
	yearSet := make(map[int]bool)
	for _, c := range charges {
		y := yearOf(c.day)
		a := e.acct(y)
		if c.carried {
			a.cur.pendCar++
		} else {
			a.cur.pendCur++
		}
		yearSet[y] = true
	}
	for y := range yearSet {
		e.touch(y, now)
	}
	id := s.nextID.Add(1)
	rec := &leaveRec{id: id, from: from, to: to, status: StatusPending, charges: charges}
	e.leaves[id] = rec
	e.intervals.insert(from, to, id)
	s.commitClock(now)

	out := make([]Charge, len(charges))
	for i, c := range charges {
		src := Current
		if c.carried {
			src = Carried
		}
		out[i] = Charge{Day: c.day, Year: yearOf(c.day), Source: src}
	}
	return id, out, nil
}

// findLeave 取假单并做存在性检查。
func (e *employee) findLeave(id int64) (*leaveRec, *Error) {
	rec, ok := e.leaves[id]
	if !ok {
		return nil, errf(CatInvalidState, "leave %d not found", id)
	}
	return rec, nil
}

// Approve 批准待批准假单：占用转为已确认使用。
func (s *Service) Approve(now int, empID string, leaveID int64) error {
	return s.withLeave(now, empID, leaveID, func(e *employee, rec *leaveRec) error {
		if rec.status != StatusPending {
			return errf(CatInvalidState, "leave %d is %s, not pending", leaveID, rec.status)
		}
		s.pinCarries(e, yearOf(now), now)
		yearSet := make(map[int]bool)
		for _, c := range rec.charges {
			y := yearOf(c.day)
			a := e.acct(y)
			if c.carried {
				a.cur.pendCar--
				a.cur.usedCar++
			} else {
				a.cur.pendCur--
				a.cur.usedCur++
			}
			yearSet[y] = true
		}
		for y := range yearSet {
			e.touch(y, now)
		}
		rec.status = StatusApproved
		return nil
	})
}

// Reject 驳回待批准假单，释放占用。
func (s *Service) Reject(now int, empID string, leaveID int64) error {
	return s.terminatePending(now, empID, leaveID, StatusRejected)
}

// Withdraw 撤回待批准假单，释放占用。
func (s *Service) Withdraw(now int, empID string, leaveID int64) error {
	return s.terminatePending(now, empID, leaveID, StatusWithdrawn)
}

func (s *Service) terminatePending(now int, empID string, leaveID int64, st Status) error {
	return s.withLeave(now, empID, leaveID, func(e *employee, rec *leaveRec) error {
		if rec.status != StatusPending {
			return errf(CatInvalidState, "leave %d is %s, not pending", leaveID, rec.status)
		}
		s.pinCarries(e, yearOf(now), now)
		s.releaseCharges(e, rec.charges, now, true)
		rec.status = st
		e.intervals.remove(rec.from)
		return nil
	})
}

// CancelLeave 整单销假：仅限尚未开始（now < from）的已批准请假，全额回补。
func (s *Service) CancelLeave(now int, empID string, leaveID int64) error {
	return s.withLeave(now, empID, leaveID, func(e *employee, rec *leaveRec) error {
		if rec.status != StatusApproved {
			return errf(CatInvalidState, "leave %d is %s, not approved", leaveID, rec.status)
		}
		if now >= rec.from {
			return errf(CatInvalidState, "leave %d already started at %d, now=%d", leaveID, rec.from, now)
		}
		s.pinCarries(e, yearOf(now), now)
		s.releaseCharges(e, rec.charges, now, false)
		rec.status = StatusCancelled
		e.intervals.remove(rec.from)
		return nil
	})
}

// EarlyEnd 提前结束：仅限已开始（from <= now）的已批准请假。
// 结束日 newEnd 起不再发生的计扣日（即 (newEnd, to] 内的计扣日）全部回补。
// newEnd 可以早于 now（追溯登记提前返岗）；此时被回补的结转额度计扣日
// 若其所属年度的结转截止日已早于 now，则该部分回补作废（见设计文档）。
func (s *Service) EarlyEnd(now int, empID string, leaveID int64, newEnd int) error {
	if newEnd < 0 {
		return errf(CatInvalidParam, "new end %d is negative", newEnd)
	}
	return s.withLeave(now, empID, leaveID, func(e *employee, rec *leaveRec) error {
		if rec.status != StatusApproved {
			return errf(CatInvalidState, "leave %d is %s, not approved", leaveID, rec.status)
		}
		if rec.from > now {
			return errf(CatInvalidState, "leave %d has not started (from=%d, now=%d)", leaveID, rec.from, now)
		}
		if newEnd < rec.from {
			return errf(CatInvalidParam, "new end %d is before leave start %d", newEnd, rec.from)
		}
		if newEnd >= rec.to {
			return errf(CatInvalidParam, "new end %d must be earlier than current end %d", newEnd, rec.to)
		}
		s.pinCarries(e, yearOf(now), now)
		var kept, released []chargeRec
		for _, c := range rec.charges {
			if c.day > newEnd {
				released = append(released, c)
			} else {
				kept = append(kept, c)
			}
		}
		s.releaseCharges(e, released, now, false)
		rec.charges = kept
		rec.to = newEnd
		if n := e.intervals.find(rec.from); n != nil {
			n.to = newEnd
		}
		return nil
	})
}

// releaseCharges 把扣减记录回补到当初扣减的来源。
// 回补到结转额度时，若计扣日所属年度的结转截止日已早于 now，则作废。
// fromPending 为 true 时释放的是占用中额度，否则是已确认使用额度。
func (s *Service) releaseCharges(e *employee, charges []chargeRec, now int, fromPending bool) {
	yearSet := make(map[int]bool)
	for _, c := range charges {
		y := yearOf(c.day)
		a := e.acct(y)
		if c.carried {
			if fromPending {
				a.cur.pendCar--
			} else {
				a.cur.usedCar--
			}
			if daysPerYear*y+s.cfg.CarryDeadline < now {
				// 结转截止日已早于销假的 now：回补作废。
				a.cur.voidedCar++
			}
		} else {
			if fromPending {
				a.cur.pendCur--
			} else {
				a.cur.usedCur--
			}
		}
		yearSet[y] = true
	}
	ys := make([]int, 0, len(yearSet))
	for y := range yearSet {
		ys = append(ys, y)
	}
	sort.Ints(ys)
	for _, y := range ys {
		e.touch(y, now)
	}
}

// withLeave 是携带假单操作的公共骨架：参数校验（调用方）、时钟检查、
// 员工存在性、员工级串行化、时钟复查，最后执行 fn 并推进时钟。
func (s *Service) withLeave(now int, empID string, leaveID int64, fn func(*employee, *leaveRec) error) error {
	if now < 0 {
		return errf(CatInvalidParam, "now=%d is negative", now)
	}
	if empID == "" {
		return errf(CatInvalidParam, "empty employee id")
	}
	if leaveID <= 0 {
		return errf(CatInvalidParam, "invalid leave id %d", leaveID)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	e := s.getEmployee(empID)
	if e == nil {
		return errf(CatEmployeeNotFound, "employee %q not found", empID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	rec, err := e.findLeave(leaveID)
	if err != nil {
		return err
	}
	if err := fn(e, rec); err != nil {
		return err
	}
	s.commitClock(now)
	return nil
}
