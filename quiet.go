package ontology

type MinuteRange struct {
	Start int
	End   int
}

func (s *Service) RegisterHousehold(now int64, household string) error {
	if !nonEmptyID(household) {
		return illegal("household id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	s.households[household] = struct{}{}
	return nil
}

func (s *Service) RegisterElevator(now int64, elevator string) error {
	if !nonEmptyID(elevator) {
		return illegal("elevator id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	s.elevators[elevator] = struct{}{}
	return nil
}

func (s *Service) SetQuietRanges(now int64, ranges []MinuteRange) error {
	for _, quietRange := range ranges {
		if quietRange.Start < 0 || quietRange.Start >= int(MinutesPerDay) ||
			quietRange.End < 0 || quietRange.End >= int(MinutesPerDay) ||
			quietRange.Start == quietRange.End {
			return illegal("quiet range endpoints must be distinct minute-of-day values")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	s.quietRanges = append([]MinuteRange(nil), ranges...)
	return nil
}

func (s *Service) AddHoliday(now int64, day int64) error {
	if day < 0 {
		return illegal("holiday day must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if day <= now/MinutesPerDay {
		return timeWindow("holidays must be strictly later than the current day")
	}
	s.advanceClock(now)
	s.holidays[day] = struct{}{}
	return nil
}

func (s *Service) isQuiet(now int64) bool {
	day := now / MinutesPerDay
	if _, holiday := s.holidays[day]; holiday {
		return true
	}
	minute := int(now % MinutesPerDay)
	for _, quietRange := range s.quietRanges {
		if quietRange.Start < quietRange.End {
			if minute >= quietRange.Start && minute < quietRange.End {
				return true
			}
			continue
		}
		if minute >= quietRange.Start || minute < quietRange.End {
			return true
		}
	}
	return false
}
