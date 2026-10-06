package dr

// RegisterData 登记一条用电数据（参与者，间隔起点，电量）。
// 同一间隔重复登记：电量不同报数据冲突，相同则幂等成功。
// 已考核事件的相关数据被拒绝（已考核）；冲突检查先于已考核检查。
func (s *System) RegisterData(participant string, interval int, value int64, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if participant == "" {
		return opErr(ErrParam, "参与者不能为空")
	}
	if interval < 0 {
		return opErr(ErrParam, "间隔索引不能为负: %d", interval)
	}
	if value < 0 {
		return opErr(ErrParam, "电量不能为负: %d", value)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}

	m := s.usage[participant]
	if old, ok := m[interval]; ok {
		if old != value {
			return opErr(ErrDataConflict, "间隔 %d 已登记电量 %d，与 %d 冲突", interval, old, value)
		}
		return nil // 幂等
	}
	day := s.dayOf(interval)
	tod := s.todOf(interval)
	for _, r := range s.settledRanges[participant] {
		if day <= r.day && tod >= r.adjTod && tod < r.endTod {
			return opErr(ErrSettled, "间隔 %d 属于已考核事件的相关范围", interval)
		}
	}

	if m == nil {
		m = map[int]int64{}
		s.usage[participant] = m
	}
	m[interval] = value
	s.dataDays[participant] = insertDay(s.dataDays[participant], day)
	return nil
}

// hasComplete 判定参与者某日 [adjTod, endTod) 日内间隔数据是否齐全。
func (s *System) hasComplete(participant string, day, adjTod, endTod int) bool {
	m := s.usage[participant]
	base := day * s.ipd
	for t := adjTod; t < endTod; t++ {
		s.Stats.BaselineDataLookups++
		if _, ok := m[base+t]; !ok {
			return false
		}
	}
	return true
}

// usageAt 读取参与者某绝对间隔的电量（调用方保证存在）。
func (s *System) usageAt(participant string, interval int) int64 {
	s.Stats.BaselineDataLookups++
	return s.usage[participant][interval]
}
