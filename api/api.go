// Package api is the public entry point for ISO 8601 week-date conversion.
package api

import (
	"fmt"

	"ontology/cal"
	"ontology/isoweek"
)

// ToISO converts a Gregorian date to ISO week-year, week number, weekday.
func ToISO(y, m, d int) (isoYear, week, weekday int, err error) {
	return isoweek.ToISO(y, m, d)
}

// FromISO converts an ISO week date back to a Gregorian date.
func FromISO(isoYear, week, weekday int) (y, m, d int, err error) {
	return isoweek.FromISO(isoYear, week, weekday)
}

// WeeksInYear reports whether year y has 52 or 53 ISO weeks.
func WeeksInYear(y int) int { return isoweek.WeeksInYear(y) }

// SelfCheck round-trips every day of years [y0, y1] through ToISO and
// FromISO, and verifies each week number stays within WeeksInYear of
// its ISO week-year.
func SelfCheck(y0, y1 int) error {
	for y := y0; y <= y1; y++ {
		for m := 1; m <= 12; m++ {
			for d := 1; d <= cal.DaysInMonth(y, m); d++ {
				iy, w, wd, err := isoweek.ToISO(y, m, d)
				if err != nil {
					return err
				}
				if w < 1 || w > isoweek.WeeksInYear(iy) {
					return fmt.Errorf("api: %d-%02d-%02d -> week %d of %d out of range", y, m, d, w, iy)
				}
				yy, mm, dd, err := isoweek.FromISO(iy, w, wd)
				if err != nil {
					return err
				}
				if yy != y || mm != m || dd != d {
					return fmt.Errorf("api: round trip %d-%02d-%02d -> %d-W%02d-%d -> %d-%02d-%02d",
						y, m, d, iy, w, wd, yy, mm, dd)
				}
			}
		}
	}
	return nil
}
