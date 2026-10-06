package dr

import "math"

// RegisterData 登记一条用电数据（参与者，间隔起点，电量）。
// 同一间隔重复登记且电量不同报「数据冲突」，相同则幂等；
// 事件考核成功后到达的相关数据被拒绝并报「已考核」。
func (s *System) RegisterData(now int64, participant string, start int64, value float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start < 0 || start%s.cfg.IntervalTicks != 0 {
		return newErr(ErrKindParam, "间隔起点 %d 未对齐计量间隔 %d", start, s.cfg.IntervalTicks)
	}
	if value < 0 || math.IsNaN(value) {
		return newErr(ErrKindParam, "电量 %v 非法", value)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	m := s.data[participant]
	if m != nil {
		if old, ok := m[start]; ok {
			if old != value {
				return newErr(ErrKindDataConflict, "参与者 %s 间隔 %d 已登记电量 %v，与 %v 冲突",
					participant, start, old, value)
			}
			s.advance(now)
			return nil
		}
	}
	if start < s.watermark[participant] {
		return newErr(ErrKindSettled, "参与者 %s 已有事件考核完成，间隔 %d 的数据迟到", participant, start)
	}
	if m == nil {
		m = map[int64]float64{}
		s.data[participant] = m
	}
	m[start] = value
	s.advance(now)
	return nil
}

// MeterValue 查询一个用电数据点（只读）。
func (s *System) MeterValue(participant string, start int64) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.data[participant]
	if m == nil {
		return 0, false
	}
	v, ok := m[start]
	return v, ok
}
