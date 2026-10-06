package crew

func (s *System) NextReport(now int, personID string, segments int, qualification string, horizon int) (int, bool, *Rejection) {
	if now < 0 || personID == "" || segments < 0 || segments > 8 || horizon < now {
		return 0, false, &Rejection{Code: InvalidArgument}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	crew, exists := s.people[personID]
	if !exists {
		return 0, false, &Rejection{Code: PersonNotFound}
	}

	candidate := now
	for candidate <= horizon {
		end := candidate + 1
		trial := &storedDuty{DutyPeriod: DutyPeriod{
			PersonID:      personID,
			Start:         candidate,
			End:           end,
			Segments:      segments,
			Qualification: qualification,
		}}
		rejection := s.checkDuty(crew, trial, false, nil)
		if rejection == nil {
			return candidate, true, nil
		}

		nextCandidate := candidate + 1
		switch rejection.Code {
		case QualificationInvalid:
			return 0, false, rejection
		case OverlappingDuty:
			if blocking := s.dutyAtOrAfter(crew, candidate); blocking != nil && blocking.Start == candidate {
				nextCandidate = blocking.End
			}
		case InsufficientRest:
			previous := s.dutyBefore(crew, candidate)
			if previous != nil {
				requiredRest := s.config.MinimumRest
				if durationOf(previous) > requiredRest {
					requiredRest = durationOf(previous)
				}
				if bound := previous.End + requiredRest; bound > nextCandidate {
					nextCandidate = bound
				}
			}
			nextDuty := s.dutyAtOrAfter(crew, candidate+1)
			if nextDuty != nil && nextDuty.Start-end < s.config.MinimumRest {
				requiredAfter := s.config.MinimumRest
				if durationOf(nextDuty) > requiredAfter {
					requiredAfter = durationOf(nextDuty)
				}
				if bound := nextDuty.End + requiredAfter; bound > nextCandidate {
					nextCandidate = bound
				}
			}
		case DutyLimitExceeded:
			if s.config.dutyLimit(candidate, segments, false) > 0 {
				nextCandidate = candidate + 1
				continue
			}
			nextCandidate = s.nextPeriodBoundary(candidate)
		case SevenDayLimitExceeded:
			nextCandidate = rejection.WindowStart + SevenDays
		case TwentyEightDayLimitExceeded:
			nextCandidate = rejection.WindowStart + TwentyEightDays
		case ExtensionRuleViolated:
			nextCandidate = candidate + 1
		default:
			return 0, false, rejection
		}
		if nextCandidate <= candidate {
			return 0, false, nil
		}
		candidate = nextCandidate
	}
	return 0, false, nil
}

func (s *System) dutyAtOrAfter(crew *person, start int) *storedDuty {
	lookback := s.config.maxLookback()
	for _, duty := range crew.index.rangeDuties(start, start+3*lookback+1) {
		if duty.Start >= start {
			return duty
		}
	}
	return nil
}

func (s *System) dutyBefore(crew *person, start int) *storedDuty {
	lookback := s.config.maxLookback()
	var previous *storedDuty
	for _, duty := range crew.index.rangeDuties(start-3*lookback, start) {
		if duty.Start < start && (previous == nil || duty.Start > previous.Start) {
			previous = duty
		}
	}
	return previous
}

func (s *System) nextPeriodBoundary(start int) int {
	minute := start % DayLength
	switch {
	case minute < s.config.EarlyEnd:
		return start + s.config.EarlyEnd - minute
	case minute < s.config.NightStart:
		return start + s.config.NightStart - minute
	default:
		return start + DayLength - minute + s.config.EarlyEnd
	}
}
