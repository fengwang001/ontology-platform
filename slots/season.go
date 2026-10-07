package slots

import "time"

// Return 返还系列中某些周。返还的周立即释放容量，并按等候名单次序
// 触发该小时段的等候申请分配；一次返还只释放被返还的那些周。
// 在返还截止时刻前返还的周不计入使用率分母，之后返还的周计入分母但不计分子。
func (c *Coordinator) Return(now time.Time, carrier string, seriesID int, weeks []int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(weeks) == 0 {
		return ErrInvalidParam
	}
	seen := map[int]bool{}
	for _, w := range weeks {
		if w < 1 || w > c.cfg.TotalWeeks || seen[w] {
			return ErrInvalidParam
		}
		seen[w] = true
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	s, err := c.findSeries(seriesID, carrier)
	if err != nil {
		return err
	}
	if c.settled {
		return ErrSeasonSettled
	}
	for _, w := range weeks {
		if w < s.StartWeek || w > s.EndWeek {
			return ErrInvalidParam
		}
		off := w - s.StartWeek
		if s.returned[off] != 0 || s.status[off] != Unregistered {
			return ErrInvalidParam
		}
	}
	before := now.Before(c.cfg.ReturnDeadline)
	slot := s.Weekday*24 + s.Hour
	for _, w := range weeks {
		if before {
			s.returned[w-s.StartWeek] = 1
		} else {
			s.returned[w-s.StartWeek] = 2
		}
		c.rem[c.cellIndex(w, s.Weekday, s.Hour)]++
		c.promote(slot, w)
	}
	c.advance(now)
	return nil
}

// Swap 两家公司交换两个同周数范围的系列。交换后各自的历史使用率记在
// 接收方名下（系列随带执行记录易主）。双方都须持有各自系列，且双方的
// 系列所在周都未执行过。
func (c *Coordinator) Swap(now time.Time, carrierA string, idA int, carrierB string, idB int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if carrierA == "" || carrierB == "" || carrierA == carrierB || idA == idB {
		return ErrInvalidParam
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	sA, err := c.findSeries(idA, carrierA)
	if err != nil {
		return err
	}
	sB, err := c.findSeries(idB, carrierB)
	if err != nil {
		return err
	}
	if sA.StartWeek != sB.StartWeek || sA.EndWeek != sB.EndWeek {
		return ErrInvalidParam
	}
	if c.settled {
		return ErrSeasonSettled
	}
	for _, s := range []*Series{sA, sB} {
		for _, st := range s.status {
			if st == Executed {
				return ErrWeekExecuted
			}
		}
	}
	sA.Holder, sB.Holder = sB.Holder, sA.Holder
	c.advance(now)
	return nil
}

// RegisterWeek 登记系列某周的执行情况。登记须在该周结束后的登记窗口内
// （窗口终点取闭），同一周只能登记一次。
func (c *Coordinator) RegisterWeek(now time.Time, carrier string, seriesID, week int, status WeekStatus) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if status != Executed && status != NotExecuted && status != Exempt {
		return ErrInvalidParam
	}
	if week < 1 || week > c.cfg.TotalWeeks {
		return ErrInvalidParam
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	s, err := c.findSeries(seriesID, carrier)
	if err != nil {
		return err
	}
	if c.settled {
		return ErrSeasonSettled
	}
	if week < s.StartWeek || week > s.EndWeek || s.returned[week-s.StartWeek] != 0 {
		return ErrInvalidParam
	}
	end := c.weekEnd(week)
	if now.Before(end) {
		return ErrWeekNotEnded
	}
	if now.After(end.Add(c.cfg.RegisterWindow)) {
		return ErrRegistrationLate
	}
	off := week - s.StartWeek
	if s.status[off] != Unregistered {
		return ErrDuplicateRegister
	}
	s.status[off] = status
	c.advance(now)
	return nil
}
