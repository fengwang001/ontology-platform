// Package cal provides proleptic Gregorian calendar basics: leap years,
// date validity, year/month lengths, ordinals and weekdays.
package cal

// IsLeap reports whether y is a leap year.
func IsLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

// DaysInYear returns 365 or 366.
func DaysInYear(y int) int {
	if IsLeap(y) {
		return 366
	}
	return 365
}

var monthDays = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// DaysInMonth returns the number of days in month m (1..12) of year y.
func DaysInMonth(y, m int) int {
	if m == 2 && IsLeap(y) {
		return 29
	}
	return monthDays[m-1]
}

// ValidDate reports whether y-m-d is a legal calendar date.
func ValidDate(y, m, d int) bool {
	return m >= 1 && m <= 12 && d >= 1 && d <= DaysInMonth(y, m)
}

// DayOfYear returns the 1-based ordinal of y-m-d within its year.
func DayOfYear(y, m, d int) int {
	n := d
	for i := 1; i < m; i++ {
		n += DaysInMonth(y, i)
	}
	return n
}

// FromDayOfYear converts ordinal n of year y back to a calendar date;
// n outside 1..DaysInYear(y) rolls into the adjacent years.
func FromDayOfYear(y, n int) (yy, m, d int) {
	for n < 1 {
		y--
		n += DaysInYear(y)
	}
	for n > DaysInYear(y) {
		n -= DaysInYear(y)
		y++
	}
	for m = 1; n > DaysInMonth(y, m); m++ {
		n -= DaysInMonth(y, m)
	}
	return y, m, n
}

// DayOfWeek returns the weekday of y-m-d: 1 = Monday .. 7 = Sunday.
// 0001-01-01 was a Monday, so day count mod 7 gives the weekday directly.
func DayOfWeek(y, m, d int) int {
	days := 365*(y-1) + (y-1)/4 - (y-1)/100 + (y-1)/400 + DayOfYear(y, m, d)
	return (days-1)%7 + 1
}
