package speq

// 日历换算采用公历（proleptic Gregorian）：
// 序号 0 固定为公历 0001-01-01。日期均为整数日序号。

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

var daysBeforeMonth = [13]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334, 365}

func daysInMonth(year, month int) int {
	if month == 2 && isLeap(year) {
		return 29
	}
	return daysBeforeMonth[month] - daysBeforeMonth[month-1]
}

// DateToOrdinal 将公历年月日换算为整数日序号。
func DateToOrdinal(year, month, day int) (int, bool) {
	if year < 1 || month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) {
		return 0, false
	}
	ordinal := 0
	for y := 1; y < year; y++ {
		if isLeap(y) {
			ordinal += 366
		} else {
			ordinal += 365
		}
	}
	ordinal += daysBeforeMonth[month-1]
	if month > 2 && isLeap(year) {
		ordinal++
	}
	return ordinal + day - 1, true
}

// OrdinalToDate 将整数日序号换算为公历年月日。
func OrdinalToDate(ordinal int) (year, month, day int, ok bool) {
	if ordinal < 0 {
		return 0, 0, 0, false
	}
	year = 1
	for {
		yearDays := 365
		if isLeap(year) {
			yearDays = 366
		}
		if ordinal < yearDays {
			break
		}
		ordinal -= yearDays
		year++
	}
	month = 1
	for month < 12 {
		if ordinal < daysBeforeMonth[month] || (month >= 2 && isLeap(year) && ordinal < daysBeforeMonth[month]+1) {
			break
		}
		month++
	}
	base := daysBeforeMonth[month-1]
	if month > 2 && isLeap(year) {
		base++
	}
	return year, month, ordinal - base + 1, true
}

// AddCalendarMonths 返回 ordinal 加上 months 个公历月后的日序号；
// 目标月份没有对应日号时取该月最后一天。
func AddCalendarMonths(ordinal int, months int) int {
	year, month, day, ok := OrdinalToDate(ordinal)
	if !ok {
		panic("speq: invalid ordinal")
	}
	month0 := month - 1 + months
	year += month0 / 12
	month0 %= 12
	if month0 < 0 {
		month0 += 12
		year--
	}
	month = month0 + 1
	if day > daysInMonth(year, month) {
		day = daysInMonth(year, month)
	}
	ord, _ := DateToOrdinal(year, month, day)
	return ord
}
