package slotcoord

import "sort"

// ReturnWeeks 返还系列中的若干周。返还的周立即释放容量并按等候名单
// 次序触发分配, 一次返还只释放所给的每一周。在返还截止时刻(含)前返还
// 的周不计入使用率分母; 之后返还的周计入分母而不计入分子。
func (s *System) ReturnWeeks(now int64, airline string, seriesID int, weeks []int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ser, found := s.series[seriesID]
	if found && ser.Holder != airline {
		found = false
	}
	var paramErr error
	if len(weeks) == 0 {
		paramErr = ErrParam
	}
	seen := map[int]bool{}
	for _, w := range weeks {
		if w < 0 || w >= s.cfg.Weeks || seen[w] {
			paramErr = ErrParam
		}
		seen[w] = true
	}
	if found {
		for _, w := range weeks {
			if w < ser.StartWeek || w > ser.EndWeek {
				paramErr = ErrParam
				continue
			}
			if st := ser.week(w); st.returned || st.registered || st.exempt {
				paramErr = ErrParam // 该周已返还/已登记/已豁免
			}
		}
	}
	var notFoundErr error
	if !found {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if s.settled {
		phaseErr = ErrSeasonSettled
	}
	if err := firstError(paramErr, s.clockErr(now), notFoundErr, phaseErr); err != nil {
		return err
	}
	s.accept(now)
	sorted := append([]int(nil), weeks...)
	sort.Ints(sorted)
	for _, w := range sorted {
		st := ser.week(w)
		st.returned = true
		st.beforeDeadline = now <= s.cfg.ReturnDeadline
		s.log("系列 %d 返还第 %d 周(截止前=%v)", ser.ID, w, st.beforeDeadline)
		s.releaseCell(Cell{Week: w, Slot: ser.slot()})
	}
	return nil
}

// SwapSeries 交换两个同周数范围的系列。交换须双方都持有且双方的系列
// 所在周都未执行过; 交换后各自的历史使用率记在接收方名下。
func (s *System) SwapSeries(now int64, seriesAID, seriesBID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, foundA := s.series[seriesAID]
	b, foundB := s.series[seriesBID]
	var paramErr error
	if seriesAID == seriesBID {
		paramErr = ErrParam
	}
	if foundA && foundB {
		if a.Holder == b.Holder {
			paramErr = ErrParam // 须为两家公司
		}
		if a.StartWeek != b.StartWeek || a.EndWeek != b.EndWeek {
			paramErr = ErrParam // 周数范围不同
		}
	}
	var notFoundErr error
	if !foundA || !foundB {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if s.settled {
		phaseErr = ErrSeasonSettled
	} else if foundA && foundB {
		for i := range a.weeks {
			if a.weeks[i].executed {
				phaseErr = ErrWeekExecuted
				break
			}
		}
		if phaseErr == nil {
			for i := range b.weeks {
				if b.weeks[i].executed {
					phaseErr = ErrWeekExecuted
					break
				}
			}
		}
	}
	if err := firstError(paramErr, s.clockErr(now), notFoundErr, phaseErr); err != nil {
		return err
	}
	s.accept(now)
	a.Holder, b.Holder = b.Holder, a.Holder
	s.log("系列 %d 与系列 %d 交换, 持有者变为 %s 与 %s", a.ID, b.ID, a.Holder, b.Holder)
	return nil
}

// RegisterExecution 由持有公司登记某周已执行或未执行。登记须在该周
// 结束后的登记窗口内(窗口终点取闭), 同一周只能登记一次。
func (s *System) RegisterExecution(now int64, airline string, seriesID, week int, executed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ser, found := s.series[seriesID]
	if found && ser.Holder != airline {
		found = false
	}
	weekValid := week >= 0 && week < s.cfg.Weeks
	var paramErr error
	switch {
	case !weekValid:
		paramErr = ErrParam
	case found && (week < ser.StartWeek || week > ser.EndWeek):
		paramErr = ErrParam // 周不在系列范围内
	case found && ser.week(week).returned:
		paramErr = ErrParam // 该周已返还
	case now < s.cfg.weekEnd(week):
		paramErr = ErrParam // 该周尚未结束
	}
	var notFoundErr error
	if !found {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if s.settled {
		phaseErr = ErrSeasonSettled
	}
	var lateErr error
	if weekValid && now > s.cfg.weekEnd(week)+s.cfg.RegistrationWindow {
		lateErr = ErrRegistrationLate
	}
	var dupErr error
	if found && weekValid && week >= ser.StartWeek && week <= ser.EndWeek && ser.week(week).registered {
		dupErr = ErrDuplicateRegistration
	}
	if err := firstError(paramErr, s.clockErr(now), notFoundErr, phaseErr, lateErr, dupErr); err != nil {
		return err
	}
	s.accept(now)
	st := ser.week(week)
	st.registered = true
	st.executed = executed
	s.log("系列 %d 第 %d 周登记为已执行=%v", ser.ID, week, executed)
	return nil
}

// RegisterExemption 登记某周为被认可的豁免(不可抗力), 该周既不计入
// 使用率分子也不计入分母。
func (s *System) RegisterExemption(now int64, airline string, seriesID, week int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ser, found := s.series[seriesID]
	if found && ser.Holder != airline {
		found = false
	}
	var paramErr error
	switch {
	case week < 0 || week >= s.cfg.Weeks:
		paramErr = ErrParam
	case found && (week < ser.StartWeek || week > ser.EndWeek):
		paramErr = ErrParam
	case found && ser.week(week).returned:
		paramErr = ErrParam
	case found && ser.week(week).exempt:
		paramErr = ErrParam // 重复豁免
	}
	var notFoundErr error
	if !found {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if s.settled {
		phaseErr = ErrSeasonSettled
	}
	if err := firstError(paramErr, s.clockErr(now), notFoundErr, phaseErr); err != nil {
		return err
	}
	s.accept(now)
	ser.week(week).exempt = true
	s.log("系列 %d 第 %d 周登记为豁免", ser.ID, week)
	return nil
}

// SettleSeason 在航季结束时为每个系列计算使用率, 产生下一航季的历史
// 资格清单。结算前所有未登记的周按未执行处理; 结算后本航季不可再登记
// 或返还。
func (s *System) SettleSeason(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var paramErr, phaseErr error
	switch {
	case s.settled:
		phaseErr = ErrSeasonSettled
	case !s.closed:
		paramErr = ErrParam // 申请尚未截止
	case now < s.cfg.seasonEnd():
		paramErr = ErrParam // 航季尚未结束
	}
	if err := firstError(paramErr, s.clockErr(now), phaseErr); err != nil {
		return err
	}
	s.accept(now)
	s.settled = true
	ids := make([]int, 0, len(s.series))
	for id := range s.series {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var list []Eligibility
	for _, id := range ids {
		ser := s.series[id]
		executed, planned := s.usage(ser)
		if historicEligible(executed, planned, s.cfg.HistoricThresholdPct) {
			list = append(list, Eligibility{
				Airline:   ser.Holder,
				Weekday:   ser.Weekday,
				Hour:      ser.Hour,
				StartWeek: ser.StartWeek,
				EndWeek:   ser.EndWeek,
			})
			s.log("系列 %d 使用率 %d/%d 达标, 公司 %s 获得历史资格", ser.ID, executed, planned, ser.Holder)
		} else {
			s.log("系列 %d 使用率 %d/%d 未达标", ser.ID, executed, planned)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Airline != b.Airline {
			return a.Airline < b.Airline
		}
		if a.Weekday != b.Weekday {
			return a.Weekday < b.Weekday
		}
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		return a.StartWeek < b.StartWeek
	})
	s.eligibility = list
	return nil
}

// StartNextSeason 在上一航季结算后开启新航季, 上一航季的历史资格
// 清单成为本航季的历史优先权依据。
func (s *System) StartNextSeason(now int64, cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var paramErr error
	if !s.settled {
		paramErr = ErrParam // 上一航季尚未结算
	}
	if err := cfg.validate(); err != nil {
		paramErr = err
	}
	if err := firstError(paramErr, s.clockErr(now)); err != nil {
		return err
	}
	s.accept(now)
	s.cfg = cfg
	s.historic = make(map[Eligibility]bool, len(s.eligibility))
	for _, e := range s.eligibility {
		s.historic[e] = true
	}
	s.requests = nil
	s.series = map[int]*Series{}
	s.nextID = 0
	s.remaining = map[Cell]int{}
	s.reserve = nil
	s.waitOrder = map[Slot][]int{}
	s.waitEntry = map[int]*waitEntry{}
	s.cellWaiters = map[Cell]map[int]bool{}
	s.closed = false
	s.settled = false
	s.eligibility = nil
	s.log("新航季开始, 继承历史资格 %d 项", len(s.historic))
	return nil
}

// usage 计算单个系列的使用率分子(实际执行次数)与分母(计划次数)。
// 计划次数 = 系列周数 - 返还截止时刻前返还的周数 - 被认可豁免的周数;
// 返还截止之后返还或未执行的周计入分母而不计入分子。开销仅随该系列
// 周数增长(由 Stats.UsageWeekScans 可验证)。
func (s *System) usage(ser *Series) (executed, planned int) {
	for i := range ser.weeks {
		s.stats.UsageWeekScans++
		st := &ser.weeks[i]
		if st.exempt {
			continue
		}
		if st.returned && st.beforeDeadline {
			continue
		}
		planned++
		if st.executed {
			executed++
		}
	}
	return executed, planned
}

// historicEligible 判定使用率是否达标: 使用率不低于达标比例(恰等于
// 视为达标), 分母为零时视为达标。
func historicEligible(executed, planned, thresholdPct int) bool {
	if planned == 0 {
		return true
	}
	return executed*100 >= thresholdPct*planned
}
