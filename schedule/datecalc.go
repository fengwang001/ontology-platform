package schedule

import "fmt"

// parseDate 解析并校验公历 "YYYY-MM-DD"，年份限 1970..2200。
// 任何格式或日历错误（含 2 月 30 日之类）都返回带原因码的 *Error。
func parseDate(s string) (int, error) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return 0, errf(ErrDateFormat, "date %q must be YYYY-MM-DD", s)
	}
	year, ok1 := atoi4(s[0:4])
	month, ok2 := atoi2(s[5:7])
	day, ok3 := atoi2(s[8:10])
	if !ok1 || !ok2 || !ok3 {
		return 0, errf(ErrDateFormat, "date %q must be YYYY-MM-DD", s)
	}
	if year < minYear || year > maxYear {
		return 0, errf(ErrDateRange, "year %d out of range %d..%d", year, minYear, maxYear)
	}
	if month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) {
		return 0, errf(ErrDateRange, "date %s does not exist in Gregorian calendar", s)
	}
	return daysBeforeYear(year) + daysBeforeMonth(year, month) + day, nil
}

// formatDate 把内部序号格式化为 "YYYY-MM-DD"（仅用于合法范围内的日期）。
func formatDate(ord int) string {
	year := yearAtOrBefore(ord)
	rest := ord - daysBeforeYear(year)
	month := 1
	for month < 12 && rest > daysBeforeMonth(year, month+1) {
		month++
	}
	day := rest - daysBeforeMonth(year, month)
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

// isoWeekday 返回 ISO 星期：周一 1 ... 周日 7。
// 序号对 7 取模恰好以周日为 0（锚点 1970-01-01 周四，余数 4）。
func isoWeekday(ord int) int {
	d := ord % 7
	if d < 0 {
		d += 7
	}
	if d == 0 {
		return 7
	}
	return d
}

// nthWeekdayOfMonth 返回 (year, month) 内第 nth 个星期 w 的日期序号；
// nth 为 -1 时取该月最后一个星期 w。该月不存在第 5 个星期 w 时第二个返回值为 false。
func nthWeekdayOfMonth(year, month, nth, w int) (int, bool) {
	dim := daysInMonth(year, month)
	first := daysBeforeYear(year) + daysBeforeMonth(year, month) + 1
	firstW := isoWeekday(first)
	var day int
	if nth >= 1 {
		day = 1 + (w-firstW+7)%7 + 7*(nth-1)
		if day > dim {
			return 0, false
		}
	} else {
		last := first + dim - 1
		lastW := isoWeekday(last)
		day = dim - (lastW-w+7)%7
	}
	return first + day - 1, true
}

// addMonths 在年/月维度上平移 delta 个月（日不参与，结果为该月第一天的年月日）。
func addMonths(year, month, delta int) (int, int) {
	m0 := (year)*12 + (month - 1) + delta
	return m0 / 12, m0%12 + 1
}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	default:
		if isLeap(year) {
			return 29
		}
		return 28
	}
}

// daysBeforeMonth 返回 year 年 month 月 1 日之前（同年内）的天数，month 取 1..12。
func daysBeforeMonth(year, month int) int {
	var d int
	for m := 1; m < month; m++ {
		d += daysInMonth(year, m)
	}
	return d
}

func daysBeforeYear(year int) int {
	y := year - 1
	return 365*y + y/4 - y/100 + y/400
}

// yearAtOrBefore 由序号反推年份（二分）。
func yearAtOrBefore(ord int) int {
	lo, hi := minYear, maxYear
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if daysBeforeYear(mid) < ord {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// ordinalOf 取 (year, month, day) 的序号，调用方保证是合法公历日。
func ordinalOf(year, month, day int) int {
	return daysBeforeYear(year) + daysBeforeMonth(year, month) + day
}

// ymd 由序号反推 (year, month, day)（仅用于合法范围内的日期）。
func ymd(ord int) (int, int, int) {
	year := yearAtOrBefore(ord)
	rest := ord - daysBeforeYear(year)
	month := 1
	for month < 12 && rest > daysBeforeMonth(year, month+1) {
		month++
	}
	day := rest - daysBeforeMonth(year, month)
	return year, month, day
}

func atoi2(s string) (int, bool) {
	v := 0
	for i := 0; i < 2; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		v = v*10 + int(s[i]-'0')
	}
	return v, true
}

func atoi4(s string) (int, bool) {
	v := 0
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		v = v*10 + int(s[i]-'0')
	}
	return v, true
}
