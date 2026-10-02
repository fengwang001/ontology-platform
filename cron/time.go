package cron

// Minute at the end of the supported range: 2199-12-31 23:59.
const maxMinute = 200*365*24*60 + 49*24*60 - 1

var monthDays = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// isLeap reports whether year y is a Gregorian leap year.
func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

// daysInMonth returns the number of days of month mo in year y.
func daysInMonth(y, mo int) int {
	if mo == 2 && isLeap(y) {
		return 29
	}
	return monthDays[mo-1]
}

// validDateTime checks year/month/day/hour/minute ranges.
func validDateTime(y, mo, d, h, mi int) bool {
	if y < 2000 || y > 2199 || mo < 1 || mo > 12 || h < 0 || h > 23 || mi < 0 || mi > 59 {
		return false
	}
	return d >= 1 && d <= daysInMonth(y, mo)
}

// ToMinute converts a calendar date and time into minutes since
// 2000-01-01 00:00 (Saturday).
func ToMinute(y, mo, d, h, mi int) (int, error) {
	if !validDateTime(y, mo, d, h, mi) {
		return 0, ErrInvalidTime
	}
	t := 0
	for year := 2000; year < y; year++ {
		t += 365
		if isLeap(year) {
			t++
		}
	}
	for month := 1; month < mo; month++ {
		t += daysInMonth(y, month)
	}
	t += d - 1
	return t*1440 + h*60 + mi, nil
}

// DateTime is a broken-down calendar instant.
type DateTime struct {
	Year    int
	Month   int
	Day     int
	Hour    int
	Minute  int
	Weekday int // 0 = Sunday, ..., 6 = Saturday
}

// FromMinute converts minutes since 2000-01-01 00:00 back into a date.
func FromMinute(t int) (DateTime, error) {
	if t < 0 || t > maxMinute {
		return DateTime{}, ErrInvalidTime
	}
	day := t / 1440
	rem := t % 1440
	// 2000-01-01 was a Saturday (6); day 0 -> weekday 6.
	weekday := (day + 6) % 7
	y := 2000
	for {
		dy := 365
		if isLeap(y) {
			dy = 366
		}
		if day < dy {
			break
		}
		day -= dy
		y++
	}
	mo := 1
	for {
		dm := daysInMonth(y, mo)
		if day < dm {
			break
		}
		day -= dm
		mo++
	}
	return DateTime{
		Year:    y,
		Month:   mo,
		Day:     day + 1,
		Hour:    rem / 60,
		Minute:  rem % 60,
		Weekday: weekday,
	}, nil
}
