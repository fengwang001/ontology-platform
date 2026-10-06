package crew

import "sort"

func (s *System) checkDuty(crew *person, candidate *storedDuty, isExtension bool, excluded *storedDuty) *Rejection {
	if !s.qualificationValid(crew, candidate) {
		return &Rejection{Code: QualificationInvalid}
	}

	lookback := s.config.maxLookback()
	rangeStart := candidate.Start - SevenDays - 2*lookback
	rangeEnd := candidate.End + SevenDays + 2*lookback
	nearby := crew.index.rangeDuties(rangeStart, rangeEnd)
	var previous, next *storedDuty
	for _, existing := range nearby {
		if excluded != nil && existing.ID == excluded.ID {
			continue
		}
		if existing.Start < candidate.Start && (previous == nil || existing.Start > previous.Start) {
			previous = existing
		}
		if existing.Start > candidate.Start && (next == nil || existing.Start < next.Start) {
			next = existing
		}
		if existing.Start == candidate.Start || existing.Start <= candidate.End && candidate.Start <= existing.End {
			return &Rejection{Code: OverlappingDuty}
		}
	}

	if previous != nil {
		requiredRest := s.config.MinimumRest
		if durationOf(previous) > requiredRest {
			requiredRest = durationOf(previous)
		}
		if candidate.Start-previous.End < requiredRest {
			return &Rejection{Code: InsufficientRest}
		}
	}
	if next != nil {
		requiredRest := s.config.MinimumRest
		if durationOf(candidate) > requiredRest {
			requiredRest = durationOf(candidate)
		}
		if next.Start-candidate.End < requiredRest {
			return &Rejection{Code: InsufficientRest}
		}
	}

	if durationOf(candidate) > s.config.dutyLimit(candidate.Start, candidate.Segments, isExtension) {
		return &Rejection{Code: DutyLimitExceeded}
	}

	if isExtension {
		for _, existing := range nearby {
			if excluded != nil && existing.ID == excluded.ID {
				continue
			}
			if existing.extended && deltaWithin(existing.Start, candidate.Start, SevenDays) {
				return &Rejection{Code: ExtensionRuleViolated}
			}
		}
	}

	if windowStart, exceeded := s.cumulativeWindow(crew, candidate, excluded, SevenDays, s.config.SevenDayLimit); exceeded {
		return &Rejection{Code: SevenDayLimitExceeded, WindowStart: windowStart}
	}
	if windowStart, exceeded := s.cumulativeWindow(crew, candidate, excluded, TwentyEightDays, s.config.TwentyEightDayLimit); exceeded {
		return &Rejection{Code: TwentyEightDayLimitExceeded, WindowStart: windowStart}
	}
	return nil
}

func (c Config) maxLookback() int {
	longest := c.EarlyLimit
	for _, limit := range []int{c.DayLimit, c.NightLimit} {
		if limit > longest {
			longest = limit
		}
	}
	longest += c.MaximumExtension
	if longest > c.MinimumRest {
		return longest
	}
	return c.MinimumRest
}

func (s *System) qualificationValid(crew *person, duty *storedDuty) bool {
	if duty.Qualification == "" {
		return true
	}
	expiry, ok := crew.qualifications[duty.Qualification]
	return ok && expiry > duty.End
}

func deltaWithin(a, b, width int) bool {
	delta := a - b
	if delta < 0 {
		delta = -delta
	}
	return delta < width
}

func (s *System) cumulativeWindow(crew *person, candidate *storedDuty, excluded *storedDuty, width, limit int) (int, bool) {
	lookback := s.config.maxLookback()
	from := candidate.Start - width - 2*lookback
	until := candidate.End + width + 2*lookback
	existing := crew.index.rangeDuties(from, until)
	events := make(map[int]int)
	addEvents := func(start, end int) {
		events[start-width]++
		events[end-width]--
		events[start]--
		events[end]++
	}
	addEvents(candidate.Start, candidate.End)
	for _, duty := range existing {
		if excluded != nil && duty.ID == excluded.ID {
			continue
		}
		addEvents(duty.Start, duty.End)
	}

	points := make([]int, 0, len(events))
	for point := range events {
		points = append(points, point)
	}
	sort.Ints(points)

	windowStart := 0
	if lower := candidate.Start - width; lower > windowStart {
		windowStart = lower
	}
	value := overlap(candidate.Start, candidate.End, windowStart, windowStart+width)
	slope := 0
	for _, duty := range existing {
		if excluded == nil || duty.ID != excluded.ID {
			value += overlap(duty.Start, duty.End, windowStart, windowStart+width)
		}
	}
	for _, point := range points {
		if point <= windowStart {
			slope += events[point]
		}
	}
	if value > limit {
		return windowStart, true
	}

	lastWindow := candidate.End
	for index := sort.SearchInts(points, windowStart+1); index < len(points); index++ {
		point := points[index]
		if point > lastWindow {
			break
		}
		if slope > 0 && value <= limit {
			crossing := windowStart + limit - value + 1
			if crossing < point {
				return crossing, true
			}
		}
		value += slope * (point - windowStart)
		if value > limit {
			return point, true
		}
		slope += events[point]
		windowStart = point
	}
	if slope > 0 && value <= limit {
		crossing := windowStart + limit - value + 1
		if crossing <= lastWindow {
			return crossing, true
		}
	}
	return 0, false
}

func overlap(fromA, untilA, fromB, untilB int) int {
	from := fromA
	if fromB > from {
		from = fromB
	}
	until := untilA
	if untilB < until {
		until = untilB
	}
	if until > from {
		return until - from
	}
	return 0
}
