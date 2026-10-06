package parking

type feeBreakdown struct {
	Base     int
	OverTime int
	Total    int
}

func ceilUnits(minutes, unit int) int {
	if minutes <= 0 || unit <= 0 {
		return 0
	}
	return (minutes + unit - 1) / unit
}

func intervalLength(a, b int) int {
	if b <= a {
		return 0
	}
	return b - a
}

type intervalSet struct {
	values [][2]int
}

func (s *intervalSet) add(start, end int) {
	if end > start {
		s.values = append(s.values, [2]int{start, end})
	}
}

func (s *intervalSet) length() int {
	return unionLength(s.values)
}

func (s *intervalSet) contains(t int) bool {
	for _, interval := range s.values {
		if t >= interval[0] && t < interval[1] {
			return true
		}
	}
	return false
}

func unionLength(intervals [][2]int) int {
	if len(intervals) == 0 {
		return 0
	}
	ordered := make([][2]int, len(intervals))
	copy(ordered, intervals)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j-1][0] > ordered[j][0]; j-- {
			ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
		}
	}
	total := 0
	start, end := ordered[0][0], ordered[0][1]
	for _, interval := range ordered[1:] {
		if interval[0] <= end && interval[1] > end {
			end = interval[1]
		} else if interval[0] > end {
			total += end - start
			start, end = interval[0], interval[1]
		}
	}
	return total + end - start
}

func calculateFee(vehicle *vehicleState, exit int, billingExit int, shareEnds [][2]int, vacateEnd int, share Interval, hasShare bool, cfg Config) feeBreakdown {
	if vehicle.MonthlyAtEntry {
		return feeBreakdown{}
	}
	entry := vehicle.Entry
	if exit <= entry {
		return feeBreakdown{}
	}
	if billingExit > exit || billingExit <= entry {
		billingExit = exit
	}
	overtime := &intervalSet{}
	for _, period := range shareEnds {
		overtime.add(max(entry, period[0]), min(billingExit, period[1]))
	}
	if vehicle.HasVacate && vehicle.VacateEnd == 0 && billingExit > vehicle.VacateDeadline {
		overtime.add(vehicle.VacateDeadline, billingExit)
	}
	if hasShare && vehicle.EnteredShared {
		for day := dayStart(entry) - Day; day <= billingExit; day += Day {
			start, end := intervalAt(share, day)
			start, end = max(entry, start), min(billingExit, end)
			if end < start {
				continue
			}
			_ = end
		}
		for minute := entry; minute < billingExit; minute++ {
			lease := &Lease{Share: share, HasShare: true}
			if !intervalOpen(lease, minute) {
				overtime.add(minute, minute+1)
			}
		}
	}
	vacatedFree := &intervalSet{}
	if vehicle.VacateStart >= 0 {
		end := min(vehicle.VacateDeadline, billingExit)
		if vacateEnd > 0 && vacateEnd < end {
			end = vacateEnd
		}
		vacatedFree.add(max(entry, vehicle.VacateStart), min(exit, end))
	}
	dailyMinutes := map[int]int{}
	for minute := entry; minute < exit; minute++ {
		if overtime.contains(minute) {
			continue
		}
		if vacatedFree.contains(minute) {
			continue
		}
		dailyMinutes[minute/Day]++
	}
	if firstDay := dailyMinutes[entry/Day]; firstDay > 0 {
		dailyMinutes[entry/Day] = max(0, firstDay-cfg.FreeMinutes)
	}
	base := 0
	for _, minutes := range dailyMinutes {
		fee := ceilUnits(minutes, cfg.BillingUnit) * cfg.UnitFee
		if cfg.DailyCap > 0 && fee > cfg.DailyCap {
			fee = cfg.DailyCap
		}
		base += fee
	}
	overtimeMinutes := overtime.length()
	return feeBreakdown{
		Base:     base,
		OverTime: overtimeMinutes * cfg.OverTimeRate,
		Total:    base + overtimeMinutes*cfg.OverTimeRate,
	}
}
