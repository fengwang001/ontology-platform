// Package isoweek converts between Gregorian dates and ISO 8601 week
// dates (week-year, week number, weekday). Week 1 is the week holding
// the year's first Thursday; a week belongs to the year of its Thursday.
package isoweek

import (
	"errors"
	"sync/atomic"

	"ontology/cal"
)

// Sentinel errors for rejected inputs, distinguishable via errors.Is.
var (
	ErrDate    = errors.New("isoweek: invalid calendar date")
	ErrWeek    = errors.New("isoweek: invalid ISO week number")
	ErrWeekday = errors.New("isoweek: invalid ISO weekday")
)

// dowCalls records how many weekday lookups the most recent ToISO call
// made. Unexported by design; observed only by in-package tests.
var dowCalls atomic.Int64

// WeeksInYear returns 52 or 53: a year has a 53rd week iff it contains
// 53 Thursdays, i.e. Jan 1 is a Thursday, or a Wednesday in a leap year.
func WeeksInYear(y int) int {
	return weeksFromJan1(cal.DayOfWeek(y, 1, 1), cal.IsLeap(y))
}

func weeksFromJan1(jan1 int, leap bool) int {
	if jan1 == 4 || (leap && jan1 == 3) {
		return 53
	}
	return 52
}

func mod7(n int) int { return (n%7 + 7) % 7 }

// ToISO converts Gregorian y-m-d to ISO week-year, week number, weekday.
func ToISO(y, m, d int) (isoYear, week, weekday int, err error) {
	if !cal.ValidDate(y, m, d) {
		return 0, 0, 0, ErrDate
	}
	dowCalls.Store(0)
	dowCalls.Add(1)
	weekday = cal.DayOfWeek(y, m, d)
	doy := cal.DayOfYear(y, m, d)
	jan1 := mod7(weekday-doy) + 1 // weekday of Jan 1, derived arithmetically
	week = (doy - weekday + 10) / 7
	isoYear = y
	switch {
	case week < 1: // belongs to the last week of the previous year
		isoYear = y - 1
		week = weeksFromJan1(mod7(jan1-1-cal.DaysInYear(isoYear))+1, cal.IsLeap(isoYear))
	case week > weeksFromJan1(jan1, cal.IsLeap(y)): // week 1 of next year
		isoYear = y + 1
		week = 1
	}
	return isoYear, week, weekday, nil
}

// FromISO converts an ISO week date back to a Gregorian date.
func FromISO(isoYear, week, weekday int) (y, m, d int, err error) {
	if weekday < 1 || weekday > 7 {
		return 0, 0, 0, ErrWeekday
	}
	if week < 1 || week > WeeksInYear(isoYear) {
		return 0, 0, 0, ErrWeek
	}
	jan1 := cal.DayOfWeek(isoYear, 1, 1)
	// Jan 4 is always in week 1, so the Monday of week 1 falls on ordinal
	// 4-(weekday of Jan 4 - 1); every other day is a fixed offset from it.
	y, m, d = cal.FromDayOfYear(isoYear, 7*(week-1)+weekday+3-mod7(jan1+2))
	return y, m, d, nil
}
