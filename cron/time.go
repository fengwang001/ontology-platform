package cron

const (
	// epochYear 是分钟计数的起始公历年。
	epochYear = 2000
	maxYear   = 2199
	minutes   = 1440
)

var monthDays = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// maxMinute 是 2199-12-31 23:59 对应的分钟数。
var maxMinute = func() int64 {
	t, err := ToMinute(maxYear, 12, 31, 23, 59)
	if err != nil {
		panic(err)
	}
	return t
}()

// isLeap 按公历规则（100 的倍数非闰、400 的倍数闰）判断闰年。
func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// daysInMonth 返回给定年月的天数。
func daysInMonth(year, month int) int {
	if month == 2 && isLeap(year) {
		return 29
	}
	return monthDays[month-1]
}

func validCalendar(year, month, day, hour, minute int) bool {
	if year < epochYear || year > maxYear {
		return false
	}
	if month < 1 || month > 12 {
		return false
	}
	if day < 1 || day > daysInMonth(year, month) {
		return false
	}
	return hour >= 0 && hour < 24 && minute >= 0 && minute < 60
}

// daysBeforeYear 返回 epochYear 年初到 year 年初之间的天数。
func daysBeforeYear(year int) int64 {
	days := 0
	for y := epochYear; y < year; y++ {
		if isLeap(y) {
			days += 366
		} else {
			days += 365
		}
	}
	return int64(days)
}

// ToMinute 把公历日期时间换算为自 2000-01-01 00:00 起的分钟数。
// 越界（含 2 月 30 日这类非法公历日期）报 ErrIllegalTime。
func ToMinute(year, month, day, hour, minute int) (int64, error) {
	if !validCalendar(year, month, day, hour, minute) {
		return 0, ErrIllegalTime
	}
	days := daysBeforeYear(year)
	for m := 1; m < month; m++ {
		days += int64(daysInMonth(year, m))
	}
	days += int64(day - 1)
	return days*minutes + int64(hour)*60 + int64(minute), nil
}

func yearFromDays(days int64) (year int, rest int64) {
	year = epochYear
	for {
		yd := 365
		if isLeap(year) {
			yd = 366
		}
		if days < int64(yd) {
			return year, days
		}
		days -= int64(yd)
		year++
	}
}

// FromMinute 是 ToMinute 的逆运算；t 超出 2000 至 2199 年范围时报错。
func FromMinute(t int64) (year, month, day, hour, minute int, err error) {
	if t < 0 || t > maxMinute {
		return 0, 0, 0, 0, 0, ErrIllegalTime
	}
	days := t / minutes
	rem := t % minutes
	year, rest := yearFromDays(days)
	month = 1
	for month < 12 {
		dm := daysInMonth(year, month)
		if rest < int64(dm) {
			break
		}
		rest -= int64(dm)
		month++
	}
	day = int(rest) + 1
	hour = int(rem / 60)
	minute = int(rem % 60)
	return year, month, day, hour, minute, nil
}
