package transformer

// AddCapacityRecord 登记一条新的变压器容量记录（生效时刻须不早于当前时刻）。
// 先用仅已确认的占用校验 [生效时刻, 下一条记录生效时刻)：任一时刻已确认之和
// 超过新容量则整体拒绝并报首个超出时刻（占位不计入）；接受后再逐时刻检查
// 占位，按创建时刻从晚到早取消，直到该时刻不再超出。
func (s *System) AddCapacityRecord(effectiveAt, capacity int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if effectiveAt < s.now {
		return &Error{Category: ErrInvalidParam, Detail: "生效时刻早于当前时刻"}
	}
	if capacity < 0 {
		return &Error{Category: ErrInvalidParam, Detail: "容量为负"}
	}
	// 新记录只影响 [effectiveAt, hi) 的容量。
	hi := s.maxActiveEndLocked()
	if next, ok := s.records.nextAfter(effectiveAt); ok && next < hi {
		hi = next
	}
	for t := effectiveAt; t < hi; t++ {
		if s.confirmedOcc.at(t) > capacity {
			return &Error{Category: ErrCapacity, Level: LevelTransformer, Time: t, Detail: "已确认预约超出新容量，变更被拒绝"}
		}
	}
	s.records.set(effectiveAt, capacity)
	for t := effectiveAt; t < hi; t++ {
		for s.transOcc.at(t) > s.records.at(t, nil) {
			r := s.latestHoldAtLocked(t)
			if r == nil {
				break // 仅剩已确认占用，必然不超出（上一步已校验）
			}
			s.removeOccupancyLocked(r)
			r.Status = StatusCancelled
		}
	}
	return nil
}

// maxActiveEndLocked 返回当前仍占用的预约的最大结束时刻。
func (s *System) maxActiveEndLocked() int {
	maxEnd := 0
	for _, r := range s.res {
		if (r.Status == StatusHolding || r.Status == StatusConfirmed) && r.End > maxEnd {
			maxEnd = r.End
		}
	}
	return maxEnd
}

// latestHoldAtLocked 返回 t 时刻占用中创建时刻最晚（并列取 ID 最大）的占位预约。
func (s *System) latestHoldAtLocked(t int) *Reservation {
	var best *Reservation
	for _, r := range s.res {
		if r.Status != StatusHolding || r.Start > t || t >= r.End {
			continue
		}
		if best == nil || r.CreatedAt > best.CreatedAt ||
			(r.CreatedAt == best.CreatedAt && r.ID > best.ID) {
			best = r
		}
	}
	return best
}
