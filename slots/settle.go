package slots

// usage 计算单个系列的使用率分子（实际执行次数）与分母（计划次数）。
// 计划次数 = 系列周数 - 截止前返还周数 - 豁免周数；分母为零时视为达标。
// 开销仅随系列周数增长。
func (e *Engine) usage(s *Series) (executed, planned int) {
	for w := s.StartWeek; w <= s.EndWeek; w++ {
		e.stats.WeekReads++
		switch s.weekState(w) {
		case WeekExecuted:
			executed++
			planned++
		case WeekPlanned, WeekUnexecuted, WeekReturnedLate:
			planned++
		}
	}
	return executed, planned
}

// Usage 返回某系列的使用率分子与分母（只读）。
func (e *Engine) Usage(seriesID int) (int, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.series[seriesID]
	if !ok {
		return 0, 0, reject(ReasonNotFound, "系列 %d 不存在", seriesID)
	}
	ex, pl := e.usage(s)
	return ex, pl, nil
}

func (e *Engine) qualified(s *Series) bool {
	ex, pl := e.usage(s)
	return pl == 0 || ex*100 >= pl*e.cfg.HistoricThresholdPercent
}

// Settle 航季结算：未登记的周按未执行处理，为每个系列计算使用率，
// 产生下一航季历史资格清单；结算后本航季不可再登记或返还。
func (e *Engine) Settle(now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	switch e.phase {
	case phaseSettled:
		return reject(ReasonSeasonSettled, "航季已结算")
	case phaseRequests:
		return reject(ReasonPhaseMismatch, "尚未执行截止分配")
	}
	if now < e.cfg.seasonEnd() {
		return reject(ReasonPhaseMismatch, "航季尚未结束")
	}
	list := []HistoricKey{}
	for _, id := range e.seriesOrder {
		s := e.series[id]
		for w := s.StartWeek; w <= s.EndWeek; w++ {
			if s.weekState(w) == WeekPlanned {
				s.Weeks[w] = WeekUnexecuted // 未登记的周按未执行处理
			}
		}
		if e.qualified(s) {
			list = append(list, HistoricKey{
				Airline: s.Airline, Weekday: s.Weekday, Hour: s.Hour,
				StartWeek: s.StartWeek, EndWeek: s.EndWeek,
			})
		}
	}
	e.eligibility = list
	e.phase = phaseSettled
	e.advance(now)
	return nil
}
