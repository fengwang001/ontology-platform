// Package httpdate parses HTTP timestamps in IMF-fixdate form only:
// "Mon, 02 Jan 2006 15:04:05 GMT". It is intentionally strict.
package httpdate

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Distinguishable parse failures; test with errors.Is.
var (
	ErrSpace  = errors.New("httpdate: bad whitespace")
	ErrSuffix = errors.New("httpdate: missing GMT suffix")
	ErrMonth  = errors.New("httpdate: bad month name")
	ErrDate   = errors.New("httpdate: nonexistent date")
)

var months = map[string]int{
	"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6,
	"Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12,
}

func daysIn(month, year int) int {
	switch month {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	}
	return 31
}

func num(s string, digits int) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && len(s) == digits
}

// Parse parses s strictly. Failures classify into the Err* sentinels.
func Parse(s string) (time.Time, error) {
	f := strings.Split(s, " ")
	if len(f) != 6 {
		return time.Time{}, ErrSpace
	}
	for _, p := range f {
		if p == "" {
			return time.Time{}, ErrSpace
		}
	}
	if len(f[0]) != 4 || !strings.HasSuffix(f[0], ",") {
		return time.Time{}, ErrSpace
	}
	if f[5] != "GMT" {
		return time.Time{}, ErrSuffix
	}
	month, ok := months[f[2]]
	if !ok {
		return time.Time{}, ErrMonth
	}
	day, ok1 := num(f[1], 2)
	year, ok2 := num(f[3], 4)
	hms := strings.Split(f[4], ":")
	if !ok1 || !ok2 || len(hms) != 3 {
		return time.Time{}, ErrDate
	}
	hour, ok3 := num(hms[0], 2)
	min, ok4 := num(hms[1], 2)
	sec, ok5 := num(hms[2], 2)
	if !ok3 || !ok4 || !ok5 {
		return time.Time{}, ErrDate
	}
	if day < 1 || day > daysIn(month, year) || hour > 23 || min > 59 || sec > 60 {
		return time.Time{}, ErrDate
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC), nil
}
