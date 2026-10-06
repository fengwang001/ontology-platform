package pricing

import "time"

type calendar struct {
	loc      *time.Location
	holidays map[string]struct{}
}

func newCalendar(loc *time.Location) calendar {
	return calendar{loc: loc, holidays: make(map[string]struct{})}
}

func (c *calendar) setHoliday(date string, holiday bool) error {
	day, err := parseHolidayDate(date, c.loc)
	if err != nil {
		return err
	}
	key := day.Format("2006-01-02")
	if holiday {
		c.holidays[key] = struct{}{}
	} else {
		delete(c.holidays, key)
	}
	return nil
}

func (c *calendar) hasHoliday(date string) bool {
	_, ok := c.holidays[date]
	return ok
}

func (c *calendar) dayType(at time.Time) DayType {
	local := at.In(c.loc)
	if _, ok := c.holidays[local.Format("2006-01-02")]; ok {
		return Holiday
	}
	switch local.Weekday() {
	case time.Saturday, time.Sunday:
		return Weekend
	default:
		return Weekday
	}
}

func parseHolidayDate(date string, loc *time.Location) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil || day.Format("2006-01-02") != date {
		return time.Time{}, errInvalid("日期格式错误")
	}
	return day, nil
}
