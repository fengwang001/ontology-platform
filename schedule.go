package ontology

import "sort"

type WeekInterval struct {
	StartSecond int64
	EndSecond   int64
}

type WeeklySchedule []WeekInterval

type scheduleVersion struct {
	intervals   WeeklySchedule
	effectiveAt int64
	slotEnd     []int64
}

type timePiece struct {
	start int64
	end   int64
	owner int
}

func validateSchedule(intervals WeeklySchedule, weekSeconds int64) error {
	if weekSeconds <= 0 {
		return &CallError{Code: ErrInvalidParameter, Message: "week seconds must be positive"}
	}
	pieces := make([]timePiece, 0, len(intervals)*2)
	for index, interval := range intervals {
		start := interval.StartSecond
		end := interval.EndSecond
		if start < 0 || start >= weekSeconds || end < 0 || end >= weekSeconds {
			return &CallError{Code: ErrInvalidParameter, Message: "schedule endpoint outside week"}
		}
		if start == end {
			if len(intervals) != 1 {
				return &CallError{Code: ErrInvalidParameter, Message: "full-week interval cannot coexist with other intervals"}
			}
			pieces = append(pieces, timePiece{0, weekSeconds, index})
			continue
		}
		if start < end {
			pieces = append(pieces, timePiece{start, end, index})
			continue
		}
		pieces = append(pieces, timePiece{start, weekSeconds, index})
		if end > 0 {
			pieces = append(pieces, timePiece{0, end, index})
		}
	}
	sort.Slice(pieces, func(i, j int) bool {
		if pieces[i].start == pieces[j].start {
			return pieces[i].end < pieces[j].end
		}
		return pieces[i].start < pieces[j].start
	})
	for i := 1; i < len(pieces); i++ {
		if pieces[i].start <= pieces[i-1].end {
			return &CallError{Code: ErrInvalidParameter, Message: "schedule intervals overlap or touch"}
		}
	}
	if len(pieces) > 1 && pieces[0].start == 0 && pieces[len(pieces)-1].end == weekSeconds && pieces[0].owner != pieces[len(pieces)-1].owner {
		return &CallError{Code: ErrInvalidParameter, Message: "schedule intervals overlap or touch across week boundary"}
	}
	return nil
}

func nextBoundary(base, at, weekSeconds int64) int64 {
	return base + (floorDiv(at-base, weekSeconds)+1)*weekSeconds
}

func floorDiv(value, divisor int64) int64 {
	quotient := value / divisor
	if (value%divisor) != 0 && ((value < 0) != (divisor < 0)) {
		quotient--
	}
	return quotient
}

func positiveMod(value, modulus int64) int64 {
	result := value % modulus
	if result < 0 {
		result += modulus
	}
	return result
}

func newScheduleVersion(intervals WeeklySchedule, submittedAt, base, weekSeconds int64) (*scheduleVersion, error) {
	if err := validateSchedule(intervals, weekSeconds); err != nil {
		return nil, err
	}
	copied := append(WeeklySchedule(nil), intervals...)
	sort.Slice(copied, func(i, j int) bool {
		return copied[i].StartSecond < copied[j].StartSecond
	})
	slotEnd := make([]int64, weekSeconds)
	for _, interval := range copied {
		start := interval.StartSecond
		end := interval.EndSecond
		switch {
		case start == end:
			for offset := int64(0); offset < weekSeconds; offset++ {
				slotEnd[offset] = weekSeconds
			}
		case start < end:
			for offset := start; offset < end; offset++ {
				slotEnd[offset] = end
			}
		default:
			for offset := start; offset < weekSeconds; offset++ {
				slotEnd[offset] = weekSeconds + end
			}
			for offset := int64(0); offset < end; offset++ {
				slotEnd[offset] = end
			}
		}
	}
	return &scheduleVersion{
		intervals:   copied,
		effectiveAt: nextBoundary(base, submittedAt, weekSeconds),
		slotEnd:     slotEnd,
	}, nil
}

func (s *scheduleVersion) intervalEndContaining(at, base, weekSeconds int64) (int64, bool) {
	relative := positiveMod(at-base, weekSeconds)
	endOffset := s.slotEnd[relative]
	if endOffset == 0 {
		return 0, false
	}
	weekStart := at - relative
	if endOffset <= relative {
		endOffset += weekSeconds
	}
	return weekStart + endOffset, true
}
