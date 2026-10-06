package transformer

import (
	"container/heap"
	"fmt"
)

// checkLocked 两级校验：区间内每一时刻，馈线占用+power 不超过馈线上限，
// 变压器总占用+power 不超过当时容量。返回首个不满足的时刻与层级；
// 同一时刻两级同时不满足时报馈线。开销只取决于区间长度。
func (s *System) checkLocked(feeder string, start, end, power int) (int, Level, bool) {
	limit := s.limits[feeder]
	occ := s.feederOcc[feeder]
	for t := start; t < end; t++ {
		s.stats.SlotVisits++
		if occ.at(t)+power > limit {
			return t, LevelFeeder, false
		}
		if s.transOcc.at(t)+power > s.records.at(t, &s.stats.CapCompares) {
			return t, LevelTransformer, false
		}
	}
	return 0, LevelNone, true
}

// Create 创建预约并进入占位状态。创建时做两级校验，不通过报容量不足。
func (s *System) Create(feeder string, start, end, power int) (int, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit, ok := s.limits[feeder]
	if !ok {
		return 0, &Error{Category: ErrInvalidParam, Detail: "馈线不存在: " + feeder}
	}
	if end <= start || start < s.now {
		return 0, &Error{Category: ErrInvalidParam, Detail: "区间非法（需 start < end 且 start 不早于当前时刻）"}
	}
	if power <= 0 {
		return 0, &Error{Category: ErrInvalidParam, Detail: "功率非正"}
	}
	if power > limit {
		return 0, &Error{Category: ErrInvalidParam, Detail: "功率超过馈线上限"}
	}
	if t, lvl, ok := s.checkLocked(feeder, start, end, power); !ok {
		return 0, &Error{Category: ErrCapacity, Level: lvl, Time: t, Detail: "创建时两级校验不通过"}
	}
	id := s.nextID
	s.nextID++
	r := &Reservation{
		ID:            id,
		Feeder:        feeder,
		Start:         start,
		End:           end,
		Power:         power,
		Status:        StatusHolding,
		CreatedAt:     s.now,
		HoldExpiresAt: s.now + s.holdDuration,
	}
	s.res[id] = r
	s.addOccupancyLocked(r)
	s.holdQueue = append(s.holdQueue, id)
	return id, nil
}

// Confirm 确认占位中的预约。确认不再校验容量（占位期间容量已被保障）。
func (s *System) Confirm(id int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.res[id]
	if !ok {
		return &Error{Category: ErrNotFound, Detail: fmt.Sprintf("预约 %d 不存在", id)}
	}
	switch r.Status {
	case StatusHolding:
		if s.now >= r.HoldExpiresAt {
			s.removeOccupancyLocked(r)
			r.Status = StatusExpired
			return &Error{Category: ErrHoldExpired, Detail: "占位已到期，预约转为已失效"}
		}
		r.Status = StatusConfirmed
		s.confirmedOcc.add(r.Start, r.End, r.Power)
		heap.Push(&s.done, endEntry{end: r.End, id: r.ID})
		if r.End <= s.now {
			s.completeLocked(r)
		}
		return nil
	case StatusExpired:
		return &Error{Category: ErrHoldExpired, Detail: "占位已到期"}
	case StatusCancelled:
		return &Error{Category: ErrCancelled, Detail: "预约已因容量下调被取消"}
	default:
		return &Error{Category: ErrInvalidState, Detail: "仅占位中的预约可确认，当前状态: " + r.Status.String()}
	}
}

// Release 释放占位中或已确认的预约，立即归还占用。
func (s *System) Release(id int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.res[id]
	if !ok {
		return &Error{Category: ErrNotFound, Detail: fmt.Sprintf("预约 %d 不存在", id)}
	}
	if r.Status != StatusHolding && r.Status != StatusConfirmed {
		return &Error{Category: ErrInvalidState, Detail: "仅占位中或已确认的预约可释放，当前状态: " + r.Status.String()}
	}
	s.removeOccupancyLocked(r)
	r.Status = StatusReleased
	return nil
}

// Modify 改约：对占位中或已确认的预约改变区间或功率。
// 校验时排除自身原占用；通过则原子替换，状态与占位到期时刻不变；
// 不通过则原预约原样保留。已开始的已确认预约只能提前结束。
func (s *System) Modify(id, start, end, power int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if end <= start || power <= 0 {
		return &Error{Category: ErrInvalidParam, Detail: "区间非法或功率非正"}
	}
	r, ok := s.res[id]
	if !ok {
		return &Error{Category: ErrNotFound, Detail: fmt.Sprintf("预约 %d 不存在", id)}
	}
	if r.Status != StatusHolding && r.Status != StatusConfirmed {
		return &Error{Category: ErrInvalidState, Detail: "仅占位中或已确认的预约可改约，当前状态: " + r.Status.String()}
	}
	if power > s.limits[r.Feeder] {
		return &Error{Category: ErrInvalidParam, Detail: "功率超过馈线上限"}
	}
	if r.Status == StatusConfirmed && r.Start <= s.now {
		// 已开始的已确认预约：只能提前结束，结束时刻不得早于当前时刻。
		if start != r.Start || power != r.Power || end > r.End || end < s.now {
			return &Error{Category: ErrInvalidParam, Detail: "已开始的已确认预约只能提前结束（起点与功率不可变，终点不早于当前时刻）"}
		}
	} else if start < s.now {
		return &Error{Category: ErrInvalidParam, Detail: "区间起点早于当前时刻"}
	}
	s.removeOccupancyLocked(r)
	if t, lvl, ok := s.checkLocked(r.Feeder, start, end, power); !ok {
		s.addOccupancyLocked(r) // 校验不通过，原预约原样保留
		return &Error{Category: ErrCapacity, Level: lvl, Time: t, Detail: "改约两级校验不通过"}
	}
	r.Start, r.End, r.Power = start, end, power
	s.addOccupancyLocked(r)
	if r.Status == StatusConfirmed {
		heap.Push(&s.done, endEntry{end: r.End, id: r.ID})
		if r.End <= s.now {
			s.completeLocked(r)
		}
	}
	return nil
}
