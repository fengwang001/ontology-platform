package slots

import "time"

// validateRequestParams 校验申请参数（不依赖已有状态的部分）。
func validateRequestParams(cfg Config, carrier string, weekday, hour, startWeek, endWeek int) error {
	if carrier == "" || weekday < 0 || weekday > 6 || hour < 0 || hour > 23 ||
		startWeek < 1 || endWeek > cfg.TotalWeeks || startWeek > endWeek ||
		endWeek-startWeek+1 < cfg.MinWeeks {
		return ErrInvalidParam
	}
	return nil
}

// consumeEligibility 若存在未使用的匹配历史资格则消费之并返回 true。
func (c *Coordinator) consumeEligibility(carrier string, weekday, hour, startWeek, endWeek int) bool {
	for i, e := range c.eligibles {
		if !c.usedElig[i] && e.Carrier == carrier && e.Weekday == weekday &&
			e.Hour == hour && e.StartWeek == startWeek && e.EndWeek == endWeek {
			c.usedElig[i] = true
			return true
		}
	}
	return false
}

// Submit 提交一条航季申请，返回申请 ID。须在申请截止时刻前提交。
func (c *Coordinator) Submit(now time.Time, carrier string, weekday, hour, startWeek, endWeek int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateRequestParams(c.cfg, carrier, weekday, hour, startWeek, endWeek); err != nil {
		return -1, err
	}
	if err := c.checkClock(now); err != nil {
		return -1, err
	}
	if c.settled {
		return -1, ErrSeasonSettled
	}
	if c.allocated || !now.Before(c.cfg.RequestDeadline) {
		return -1, ErrRequestsClosed
	}
	req := &Request{
		ID:        len(c.requests),
		Carrier:   carrier,
		Weekday:   weekday,
		Hour:      hour,
		StartWeek: startWeek,
		EndWeek:   endWeek,
		SubmitAt:  now,
	}
	req.Historic = c.consumeEligibility(carrier, weekday, hour, startWeek, endWeek)
	c.requests = append(c.requests, req)
	c.advance(now)
	return req.ID, nil
}

// checkHistoricCapacity 校验历史申请总量不超过任何单元格容量，否则属参数非法。
func (c *Coordinator) checkHistoricCapacity() error {
	need := make([]int, len(c.rem))
	for _, req := range c.requests {
		if !req.Historic {
			continue
		}
		eachCell(req.Weekday, req.Hour, req.StartWeek, req.EndWeek, func(_, idx int) {
			need[idx]++
		})
	}
	for _, n := range need {
		if n > c.cfg.Capacity {
			return ErrInvalidParam
		}
	}
	return nil
}

// fitsWithReserve 判定新进入者申请是否可满足：每格剩余容量与保留额均大于零。
func (c *Coordinator) fitsWithReserve(req *Request) bool {
	ok := true
	eachCell(req.Weekday, req.Hour, req.StartWeek, req.EndWeek, func(_, idx int) {
		c.cellChecks++
		if c.rem[idx] <= 0 || c.reserve[idx] <= 0 {
			ok = false
		}
	})
	return ok
}

// fitsBeyondReserve 判定其余申请是否可满足：每格剩余容量减去保留额仍大于零。
func (c *Coordinator) fitsBeyondReserve(req *Request) bool {
	ok := true
	eachCell(req.Weekday, req.Hour, req.StartWeek, req.EndWeek, func(_, idx int) {
		c.cellChecks++
		if c.rem[idx]-c.reserve[idx] <= 0 {
			ok = false
		}
	})
	return ok
}

// allocateRequest 把申请转为系列；held 非空时累加持有者本航季持有数。
func (c *Coordinator) allocateRequest(req *Request, held map[string]int) {
	c.newSeries(req.Carrier, req.Weekday, req.Hour, req.StartWeek, req.EndWeek)
	req.Allocated = true
	if held != nil {
		held[req.Carrier]++
	}
}

// RunAllocation 在申请截止时刻执行一次性结算，全有或全无。
// 顺序：历史优先权申请 -> 新进入者保留额内的申请 -> 其余申请；
// 未被满足的申请进入该小时段的等候名单（按提交时刻排序）。
func (c *Coordinator) RunAllocation(now time.Time) (*AllocationResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkHistoricCapacity(); err != nil {
		return nil, err
	}
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	if c.settled {
		return nil, ErrSeasonSettled
	}
	if c.allocated {
		return nil, ErrAlreadyAllocated
	}
	if now.Before(c.cfg.RequestDeadline) {
		return nil, ErrRequestsOpen
	}

	held := map[string]int{}
	// 阶段一：满足全部享有历史优先权的申请。
	for _, req := range c.requests {
		if req.Historic {
			c.allocateRequest(req, held)
		}
	}
	// 阶段二：每格剩余容量的一半（向下取整）记为新进入者保留额。
	for i := range c.rem {
		c.reserve[i] = c.rem[i] / 2
	}
	// 阶段三：新进入者申请按提交时刻早者先满足，每满足一个保留额各减一。
	for _, req := range c.requests {
		if req.Historic || req.Allocated {
			continue
		}
		if held[req.Carrier] >= c.cfg.NewEntrantThreshold {
			continue
		}
		if c.fitsWithReserve(req) {
			c.allocateRequest(req, held)
			eachCell(req.Weekday, req.Hour, req.StartWeek, req.EndWeek, func(_, idx int) {
				if c.reserve[idx] > 0 {
					c.reserve[idx]--
				}
			})
		}
	}
	// 阶段四：其余申请按提交时刻早者先处理，要求每格剩余容量减去保留额仍大于零。
	for _, req := range c.requests {
		if req.Allocated {
			continue
		}
		if c.fitsBeyondReserve(req) {
			c.allocateRequest(req, held)
		}
	}

	res := &AllocationResult{}
	for _, req := range c.requests {
		outcome := RequestOutcome{
			RequestID: req.ID,
			Carrier:   req.Carrier,
			Historic:  req.Historic,
			Allocated: req.Allocated,
		}
		if !req.Allocated {
			outcome.Reason = ErrCapacity
			slot := req.Weekday*24 + req.Hour
			c.waitlist[slot] = append(c.waitlist[slot], req.ID)
		}
		res.Outcomes = append(res.Outcomes, outcome)
	}
	c.allocated = true
	c.advance(now)
	return res, nil
}

// promote 在某（星期几, 小时段）的某周释放容量后，按等候名单次序尝试
// 把覆盖该周且仍有效的申请转为系列（全有或全无，不满足者留在名单中）。
func (c *Coordinator) promote(slot, week int) {
	ids := c.waitlist[slot]
	if len(ids) == 0 {
		return
	}
	kept := make([]int, 0, len(ids))
	for _, id := range ids {
		req := c.requests[id]
		if req.Allocated || week < req.StartWeek || week > req.EndWeek {
			kept = append(kept, id)
			continue
		}
		if c.fitsPlain(req.Weekday, req.Hour, req.StartWeek, req.EndWeek) {
			c.allocateRequest(req, nil)
		} else {
			kept = append(kept, id)
		}
	}
	c.waitlist[slot] = kept
}

// RetryRequest 由公司触发，重试其仍在等候名单中的申请：
// 覆盖的每个单元格都有剩余容量则转为系列，否则报容量不足。
func (c *Coordinator) RetryRequest(now time.Time, carrier string, requestID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if carrier == "" || requestID < 0 {
		return ErrInvalidParam
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	if requestID >= len(c.requests) || c.requests[requestID].Carrier != carrier {
		return ErrNotFound
	}
	req := c.requests[requestID]
	if c.settled {
		return ErrSeasonSettled
	}
	if !c.allocated {
		return ErrNotAllocated
	}
	if req.Allocated {
		return ErrInvalidParam
	}
	if !c.fitsPlain(req.Weekday, req.Hour, req.StartWeek, req.EndWeek) {
		return ErrCapacity
	}
	c.allocateRequest(req, nil)
	slot := req.Weekday*24 + req.Hour
	ids := c.waitlist[slot]
	kept := make([]int, 0, len(ids))
	for _, id := range ids {
		if id != requestID {
			kept = append(kept, id)
		}
	}
	c.waitlist[slot] = kept
	c.advance(now)
	return nil
}
