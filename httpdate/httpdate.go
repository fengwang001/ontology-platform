// Package httpdate parses HTTP-date timestamps in the single IMF-fixdate
// format "Mon, 02 Jan 2006 15:04:05 GMT" (RFC 7231, section 7.1.1.1).
// It depends on no other project package and does not use time.Parse.
package httpdate

import (
	"errors"
	"time"
)

// Four distinguishable parse failures.
var (
	ErrWhitespace = errors.New("httpdate: unexpected or extra whitespace")
	ErrSuffix     = errors.New(`httpdate: suffix must be "GMT"`)
	ErrMonth      = errors.New("httpdate: invalid month name")
	ErrDate       = errors.New("httpdate: calendar date does not exist")
	ErrSyntax     = errors.New("httpdate: malformed timestamp")
)

// Parse strictly parses an IMF-fixdate. There must be exactly one SP where
// the grammar requires one and no leading/trailing whitespace.
func Parse(s string) (time.Time, error) {
	if !onlyAllowedSpaces(s) {
		return time.Time{}, ErrWhitespace
	}
	if len(s) != 29 {
		return time.Time{}, ErrSyntax
	}
	if s[19] != ':' || s[22] != ':' {
		return time.Time{}, ErrSyntax
	}
	if s[26:] != "GMT" {
		return time.Time{}, ErrSuffix
	}
	weekdays := map[string]bool{"Mon": true, "Tue": true, "Wed": true,
		"Thu": true, "Fri": true, "Sat": true, "Sun": true}
	if s[3] != ',' || !weekdays[s[:3]] || !isDigit(s[5:7]) ||
		!isDigit(s[12:16]) || !isDigit(s[17:19]) ||
		!isDigit(s[20:22]) || !isDigit(s[23:25]) {
		return time.Time{}, ErrSyntax
	}
	month, ok := monthOf(s[8:11])
	if !ok {
		return time.Time{}, ErrMonth
	}
	day := int(s[5]-'0')*10 + int(s[6]-'0')
	year := atoi(s[12:16])
	hour := atoi(s[17:19])
	min := atoi(s[20:22])
	sec := atoi(s[23:25])
	if hour > 23 || min > 59 || sec > 59 || day < 1 || day > daysIn(year, month) {
		return time.Time{}, ErrDate
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC), nil
}

func onlyAllowedSpaces(s string) bool {
	want := map[int]bool{4: true, 7: true, 11: true, 16: true, 25: true}
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' || s[i] == ' ' && !want[i] {
			return false
		}
	}
	return true
}

func isDigit(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func monthOf(s string) (int, bool) {
	months := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	for i, m := range months {
		if s == m {
			return i + 1, true
		}
	}
	return 0, false
}

func daysIn(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	default:
		if year%4 == 0 && year%100 != 0 || year%400 == 0 {
			return 29
		}
		return 28
	}
}
