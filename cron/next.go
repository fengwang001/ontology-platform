package cron

// searchDays bounds the number of whole days NextFire examines.
// The spec guarantees a match within 4000*1440 minutes, i.e. at most
// 4000 candidate days after t's day; including the starting day the
// counter never exceeds 4001.
const searchDays = 4001

// NextFire returns the smallest matching minute strictly greater than t.
//
// It never scans minute by minute: days are advanced one at a time
// (bounded by searchDays), and within a located day the next allowed
// hour and minute are selected directly from the parsed sets.
func NextFire(spec *Spec, t int) (int, error) {
	if spec == nil {
		return 0, ErrInvalidArgument
	}
	if t < 0 || t > maxMinute {
		return 0, ErrInvalidTime
	}
	spec.daySteps = 0

	// Start from the day containing t+1.
	startDay := (t + 1) / 1440
	for step := 0; step < searchDays; step++ {
		spec.daySteps++
		curDay := startDay + step
		dt, err := FromMinute(curDay * 1440)
		if err != nil {
			return 0, ErrNoNextFire
		}
		if !spec.dayMatches(dt) {
			continue
		}
		// On the starting day only times strictly greater than t count.
		fromMinute := 0
		if curDay == t/1440 {
			fromMinute = t - curDay*1440 + 1
			if fromMinute >= 1440 {
				continue
			}
		}
		m, ok := nextTimeOfDay(spec, fromMinute)
		if !ok {
			continue
		}
		fire := curDay*1440 + m
		if fire > maxMinute {
			return 0, ErrNoNextFire
		}
		return fire, nil
	}
	return 0, ErrNoNextFire
}

// nextTimeOfDay returns the smallest minute-of-day >= from whose hour
// and minute are both in the spec's sets, by jumping directly inside
// the sets instead of scanning minutes.
func nextTimeOfDay(spec *Spec, from int) (int, bool) {
	fromHour := from / 60
	fromMinute := from % 60
	for h := fromHour; h < 24; h++ {
		if !spec.hours[h] {
			continue
		}
		start := 0
		if h == fromHour {
			start = fromMinute
		}
		for mi := start; mi < 60; mi++ {
			if spec.minutes[mi] {
				return h*60 + mi, true
			}
		}
	}
	return 0, false
}

// dayMatches reports whether the day described by dt satisfies the
// month plus the day-of-month / day-of-week rule.
//
// When both the day-of-month and day-of-week fields are non-wildcard
// (their text does not start with '*'), a day matches if EITHER set
// contains it. Otherwise the two sets are combined with AND.
func (s *Spec) dayMatches(dt DateTime) bool {
	if !s.months[dt.Month-1] {
		return false
	}
	dayOK := s.days[dt.Day-1]
	weekOK := s.weekdays[dt.Weekday]
	if !s.dayWild && !s.weekWild {
		return dayOK || weekOK
	}
	return dayOK && weekOK
}
