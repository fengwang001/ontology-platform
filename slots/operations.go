package slots

import "time"

// ReturnWeek 返还系列的某一周（含返还登记时刻，用于判断返还截止）。
// 返回本次因释放而从等候名单转为系列的申请 ID（无则空串）。
func (c *Coordinator) ReturnWeek(at time.Time, airline, sid string, week int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tracef(c.log, "ReturnWeek in {at:%s airline:%q series:%s week:%d}",
		at.Format(time.RFC3339), airline, sid, week)
	s := c.series[sid]
	if airline == "" || week < 1 || week > c.cfg.Weeks {
		tracef(c.log, "ReturnWeek out -> %s", ErrInvalid)
		return "", ErrInvalid
	}
	if err := c.advance(at); err != nil {
		tracef(c.log, "ReturnWeek out -> %s", err)
		return "", err
	}
	if s == nil || s.Airline != airline {
		tracef(c.log, "ReturnWeek out -> %s", ErrNotFound)
		return "", ErrNotFound
	}
	if week < s.StartWeek || week > s.EndWeek || s.returned[week] {
		tracef(c.log, "ReturnWeek out -> %s", ErrInvalid)
		return "", ErrInvalid
	}
	if c.phase == phaseSettled {
		tracef(c.log, "ReturnWeek out -> %s", ErrSeasonSettled)
		return "", ErrSeasonSettled
	}
	if c.phase != phaseAllocated {
		tracef(c.log, "ReturnWeek out -> %s", ErrApplyClosed)
		return "", ErrApplyClosed
	}

	s.returned[week] = true
	s.returnedAt[week] = at
	c.occ[cellKey{week, s.Day, s.Hour}]--
	c.commit(at)
	got := c.refill(s.Day, s.Hour, week)
	tracef(c.log, "ReturnWeek out -> released series:%s week:%d refill:%q", sid, week, got)
	return got, nil
}

// ExchangeSeries 交换两个同周数范围系列的持有权。交换后历史使用率记在接收方名下。
func (c *Coordinator) ExchangeSeries(at time.Time, airlineA, sidA, airlineB, sidB string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tracef(c.log, "ExchangeSeries in {at:%s %s/%s <-> %s/%s}",
		at.Format(time.RFC3339), airlineA, sidA, airlineB, sidB)
	sa, sb := c.series[sidA], c.series[sidB]
	if airlineA == "" || airlineB == "" {
		tracef(c.log, "ExchangeSeries out -> %s", ErrInvalid)
		return ErrInvalid
	}
	if err := c.advance(at); err != nil {
		tracef(c.log, "ExchangeSeries out -> %s", err)
		return err
	}
	if sa == nil || sb == nil {
		tracef(c.log, "ExchangeSeries out -> %s", ErrNotFound)
		return ErrNotFound
	}
	if sa == sb || sa.Airline != airlineA || sb.Airline != airlineB ||
		sa.StartWeek != sb.StartWeek || sa.EndWeek != sb.EndWeek ||
		airlineA == airlineB {
		tracef(c.log, "ExchangeSeries out -> %s", ErrInvalid)
		return ErrInvalid
	}
	if c.phase == phaseSettled {
		tracef(c.log, "ExchangeSeries out -> %s", ErrSeasonSettled)
		return ErrSeasonSettled
	}
	if c.phase != phaseAllocated {
		tracef(c.log, "ExchangeSeries out -> %s", ErrApplyClosed)
		return ErrApplyClosed
	}
	if seriesEverExecuted(sa) || seriesEverExecuted(sb) {
		tracef(c.log, "ExchangeSeries out -> %s", ErrWeekExecuted)
		return ErrWeekExecuted
	}
	sa.Airline, sb.Airline = sb.Airline, sa.Airline
	c.held[airlineA]--
	c.held[airlineB]--
	c.held[sa.Airline]++
	c.held[sb.Airline]++
	c.commit(at)
	tracef(c.log, "ExchangeSeries out -> %s now:%q %s now:%q", sidA, sa.Airline, sidB, sb.Airline)
	return nil
}

// RegisterWeek 由持有者登记某周执行情况。须在周结束后的登记窗口内，终点取闭。
func (c *Coordinator) RegisterWeek(at time.Time, airline, sid string, week int, st RegStatus) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tracef(c.log, "RegisterWeek in {at:%s airline:%q series:%s week:%d status:%d}",
		at.Format(time.RFC3339), airline, sid, week, st)
	s := c.series[sid]
	if airline == "" || week < 1 || week > c.cfg.Weeks ||
		(st != RegExecuted && st != RegNotExecuted && st != RegExempt) ||
		at.Before(c.cfg.weekEnd(week)) {
		tracef(c.log, "RegisterWeek out -> %s", ErrInvalid)
		return ErrInvalid
	}
	if err := c.advance(at); err != nil {
		tracef(c.log, "RegisterWeek out -> %s", err)
		return err
	}
	if s == nil || s.Airline != airline {
		tracef(c.log, "RegisterWeek out -> %s", ErrNotFound)
		return ErrNotFound
	}
	if week < s.StartWeek || week > s.EndWeek || s.returned[week] {
		tracef(c.log, "RegisterWeek out -> %s", ErrInvalid)
		return ErrInvalid
	}
	if c.phase == phaseSettled {
		tracef(c.log, "RegisterWeek out -> %s", ErrSeasonSettled)
		return ErrSeasonSettled
	}
	if c.phase != phaseAllocated {
		tracef(c.log, "RegisterWeek out -> %s", ErrApplyClosed)
		return ErrApplyClosed
	}
	windowEnd := c.cfg.weekEnd(week).Add(c.cfg.RegisterWindow)
	if at.After(windowEnd) {
		tracef(c.log, "RegisterWeek out -> %s", ErrRegisterLate)
		return ErrRegisterLate
	}
	if _, dup := s.register[week]; dup {
		tracef(c.log, "RegisterWeek out -> %s", ErrDupRegister)
		return ErrDupRegister
	}
	s.register[week] = st
	c.commit(at)
	tracef(c.log, "RegisterWeek out -> accepted status:%d", st)
	return nil
}

// refill 按等候名单次序为一次周释放补位：只处理该星期几/小时段名单中
// 覆盖该周且仍可整体满足的申请，一次返还最多补一个系列。
// 开销只随该小时段名单长度增长，与系列总数及航季总周数无关。
func (c *Coordinator) refill(day, hour, week int) string {
	k := slotKey{day, hour}
	list := c.wait[k]
	for i := 0; i < len(list); {
		a := list[i].app
		if week < a.StartWeek || week > a.EndWeek {
			i++
			continue
		}
		capOK := true
		for w := a.StartWeek; w <= a.EndWeek; w++ {
			ck := cellKey{w, a.Day, a.Hour}
			if c.occ[ck] >= c.cfg.Capacity[ck.day][ck.hour] {
				capOK = false
				break
			}
		}
		if !capOK {
			i++
			continue
		}
		s := c.createSeriesFromApp(a)
		c.wait[k] = append(list[:i], list[i+1:]...)
		tracef(c.log, "  refill %s -> %s airline:%q", a.ID, s.ID, a.Airline)
		return a.ID
	}
	return ""
}

func (c *Coordinator) createSeriesFromApp(a *Application) *Series {
	s := &Series{
		ID:         c.newSeriesID(),
		Airline:    a.Airline,
		Day:        a.Day,
		Hour:       a.Hour,
		StartWeek:  a.StartWeek,
		EndWeek:    a.EndWeek,
		returned:   map[int]bool{},
		returnedAt: map[int]time.Time{},
		register:   map[int]RegStatus{},
	}
	c.series[s.ID] = s
	c.held[s.Airline]++
	for w := a.StartWeek; w <= a.EndWeek; w++ {
		c.occ[cellKey{w, a.Day, a.Hour}]++
	}
	return s
}

func seriesEverExecuted(s *Series) bool {
	for _, st := range s.register {
		if st == RegExecuted {
			return true
		}
	}
	return false
}
