package crew

import (
	"errors"
	"fmt"
)

const (
	DayLength       = 24 * 60
	SevenDays       = 7 * DayLength
	TwentyEightDays = 28 * DayLength
)

// Config describes fixed rostering limits. Early, day and night periods are
// [0,EarlyEnd), [EarlyEnd,NightStart) and [NightStart,1440).
type Config struct {
	EarlyEnd            int
	NightStart          int
	EarlyLimit          int
	DayLimit            int
	NightLimit          int
	ReductionPerSegment int
	MinimumDutyLimit    int
	MinimumRest         int
	SevenDayLimit       int
	TwentyEightDayLimit int
	MaximumExtension    int
}

type DutyPeriod struct {
	PersonID      string
	ID            string
	Start         int
	End           int
	Segments      int
	Qualification string
}

type storedDuty struct {
	DutyPeriod
	extended bool
}

type RejectionCode int

const (
	Accepted RejectionCode = iota
	InvalidArgument
	ClockRollback
	PersonNotFound
	DutyNotFound
	AlreadyStartedOrReleased
	QualificationInvalid
	OverlappingDuty
	InsufficientRest
	DutyLimitExceeded
	ExtensionRuleViolated
	SevenDayLimitExceeded
	TwentyEightDayLimitExceeded
)

type Rejection struct {
	Code        RejectionCode
	WindowStart int
}

func (r Rejection) Error() string {
	switch r.Code {
	case InvalidArgument:
		return "invalid argument"
	case ClockRollback:
		return "clock rollback"
	case PersonNotFound:
		return "person does not exist"
	case DutyNotFound:
		return "duty period does not exist"
	case AlreadyStartedOrReleased:
		return "started or released duty cannot be changed"
	case QualificationInvalid:
		return "qualification is invalid"
	case OverlappingDuty:
		return "duty periods overlap"
	case InsufficientRest:
		return "rest is insufficient"
	case DutyLimitExceeded:
		return "single duty limit exceeded"
	case ExtensionRuleViolated:
		return "extension rule violated"
	case SevenDayLimitExceeded:
		return fmt.Sprintf("seven-day cumulative limit exceeded at window %d", r.WindowStart)
	case TwentyEightDayLimitExceeded:
		return fmt.Sprintf("twenty-eight-day cumulative limit exceeded at window %d", r.WindowStart)
	default:
		return "accepted"
	}
}

func (c Config) Validate() error {
	if c.EarlyEnd <= 0 || c.NightStart <= c.EarlyEnd || c.NightStart >= DayLength {
		return errors.New("period boundaries must satisfy 0 < earlyEnd < nightStart < 1440")
	}
	limits := [3]int{c.EarlyLimit, c.DayLimit, c.NightLimit}
	for _, limit := range limits {
		if limit <= 0 || limit < c.MinimumDutyLimit {
			return errors.New("duty limits must be positive and at least minimumDutyLimit")
		}
	}
	if c.ReductionPerSegment < 0 || c.MinimumDutyLimit <= 0 || c.MinimumRest < 0 ||
		c.MaximumExtension < 0 || c.SevenDayLimit <= 0 || c.TwentyEightDayLimit <= 0 ||
		c.SevenDayLimit > c.TwentyEightDayLimit {
		return errors.New("limit configuration is invalid")
	}
	return nil
}

func (c Config) baseLimit(start int) int {
	minute := start % DayLength
	switch {
	case minute < c.EarlyEnd:
		return c.EarlyLimit
	case minute < c.NightStart:
		return c.DayLimit
	default:
		return c.NightLimit
	}
}

func (c Config) dutyLimit(start, segments int, extended bool) int {
	limit := c.baseLimit(start) - segments*c.ReductionPerSegment
	if limit < c.MinimumDutyLimit {
		limit = c.MinimumDutyLimit
	}
	if extended {
		limit += c.MaximumExtension
	}
	return limit
}

func durationOf(duty *storedDuty) int { return duty.End - duty.Start }
