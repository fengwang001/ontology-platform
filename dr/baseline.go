package dr

// dayOffsets 返回事件调整期与有效窗口内各间隔相对事件日起点的偏移。
func (s *System) dayOffsets(e *Event) (adjust, window []int64) {
	dayStart := e.Params.Day * s.cfg.TicksPerDay
	for t := e.adjustStart(); t < e.Params.WindowStart; t += s.cfg.IntervalTicks {
		adjust = append(adjust, t-dayStart)
	}
	for t := e.Params.WindowStart; t < e.effectiveWindowEnd(); t += s.cfg.IntervalTicks {
		window = append(window, t-dayStart)
	}
	return adjust, window
}

func (s *System) dayComplete(participant string, day int64, offsets []int64) bool {
	base := day * s.cfg.TicksPerDay
	for _, off := range offsets {
		if _, ok := s.meterGet(participant, base+off); !ok {
			return false
		}
	}
	return true
}

// excludedDays 返回该参与者须排除的事件日集合：
// 接受过（承诺仍具约束力）且事件未被窗口前取消的日期。
func (s *System) excludedDays(participant string) map[int64]bool {
	excluded := map[int64]bool{}
	for _, c := range s.commitmentsOf(participant) {
		ev := s.events[c.EventID]
		if ev.Cancelled && !ev.CancelAfterStart {
			continue
		}
		excluded[ev.Params.Day] = true
	}
	return excluded
}

// qualifyingDays 返回事件日之前最近的若干个资格日：
// 与事件日同为工作日或休息日、非排除日、所需间隔数据齐全。
// 扫描上界为 MaxLookbackDays，开销只与资格日数量及被排除日期数相关。
func (s *System) qualifyingDays(participant string, e *Event, offsets []int64) []int64 {
	excluded := s.excludedDays(participant)
	workday := s.cfg.workday(e.Params.Day)
	var days []int64
	for d := e.Params.Day - 1; d >= 0 &&
		d >= e.Params.Day-int64(s.cfg.MaxLookbackDays) &&
		len(days) < s.cfg.QualifyingDays; d-- {
		if s.cfg.workday(d) != workday {
			continue
		}
		if excluded[d] {
			continue
		}
		if !s.dayComplete(participant, d, offsets) {
			continue
		}
		days = append(days, d)
	}
	return days
}

// baselineMeans 计算各偏移间隔在资格日上的均值，days 的顺序即扫描顺序（由近及远）。
func (s *System) baselineMeans(participant string, days []int64, offsets []int64) []float64 {
	means := make([]float64, len(offsets))
	for _, d := range days {
		base := d * s.cfg.TicksPerDay
		for i, off := range offsets {
			v, _ := s.meterGet(participant, base+off)
			means[i] += v
		}
	}
	for i := range means {
		means[i] /= float64(len(days))
	}
	return means
}

// adjustRatio 计算同日校正比例：事件日调整期实际用电与同期基线之比，
// 裁剪到配置的上下界内（取等不裁剪）；基线和为零时不做校正。
func (s *System) adjustRatio(participant string, e *Event, adjustOffs []int64, adjustMeans []float64) float64 {
	dayStart := e.Params.Day * s.cfg.TicksPerDay
	var actual, base float64
	for i, off := range adjustOffs {
		v, _ := s.meterGet(participant, dayStart+off)
		actual += v
		base += adjustMeans[i]
	}
	if base == 0 {
		return 1
	}
	ratio := actual / base
	if ratio < s.cfg.AdjRatioLower {
		ratio = s.cfg.AdjRatioLower
	}
	if ratio > s.cfg.AdjRatioUpper {
		ratio = s.cfg.AdjRatioUpper
	}
	return ratio
}
