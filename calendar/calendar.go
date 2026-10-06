// Package calendar converts between integer day serials and civil
// (Gregorian) dates, and provides calendar-month arithmetic.
//
// Day serial convention: day 0 is 1970-01-01; serials count consecutively
// in both directions (negative values are before the epoch).
// Calendar-month addition follows the Gregorian calendar: year and month
// are carried into the target month and the day-of-month is preserved;
// when the target month has no such day-of-month, the last day of that
// month is used (e.g. Jan 31 + 1 month = last day of February).
package calendar

// FromCivil converts a civil date (y, m, d) to a day serial.
// It uses Howard Hinnant's days_from_civil algorithm and is valid for
// any proleptic Gregorian year.
func FromCivil(y, m, d int) int {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400          // [0, 399]
	mp := mod(m+9, 12)          // [0, 11], March is 0
	doy := (153*mp+2)/5 + d - 1 // [0, 365]
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// ToCivil converts a day serial to a civil date (y, m, d).
func ToCivil(z int) (y, m, d int) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097                                  // [0, 146096]
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365 // [0, 399]
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100) // [0, 365]
	mp := (5*doy + 2) / 153                  // [0, 11]
	d = doy - (153*mp+2)/5 + 1               // [1, 31]
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

// AddMonths adds calendar months to a day serial (months may be negative).
// When the target month lacks the day-of-month, the last day of the target
// month is used.
func AddMonths(day, months int) int {
	y, m, d := ToCivil(day)
	total := y*12 + (m - 1) + months
	ny := floorDiv(total, 12)
	nm := total - ny*12 + 1
	nd := d
	if dim := daysInMonth(ny, nm); nd > dim {
		nd = dim
	}
	return FromCivil(ny, nm, nd)
}

// DaysInMonth returns the number of days in the given civil month.
func DaysInMonth(y, m int) int { return daysInMonth(y, m) }

func daysInMonth(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(y) {
			return 29
		}
		return 28
	}
	return 0
}

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func mod(a, b int) int {
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}
