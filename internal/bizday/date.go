package bizday

import "fmt"

const (
	minYear = 1900
	maxYear = 2999
)

// splitDate 把 YYYYMMDD 拆为年、月、日。
func splitDate(d int) (year, month, day int) {
	year = d / 10000
	month = d / 100 % 100
	day = d % 100
	return year, month, day
}

// isLeapYear 按公历规则判断闰年：
// 能被 4 整除且不能被 100 整除，或能被 400 整除。
func isLeapYear(year int) bool {
	return year%4 == 0 && year%100 != 0 || year%400 == 0
}

// daysInMonth 返回 year 年 month 月的实际天数，month 非法时返回 0。
func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9:
		return 30
	case 11:
		return 31
	case 2:
		if isLeapYear(year) {
			return 29
		}
		return 28
	default:
		return 0
	}
}

// validateDate 校验 d 是否为合法日期，非法时返回包裹 ErrInvalidDate 的错误。
func validateDate(d int) error {
	year, month, day := splitDate(d)
	if year < minYear || year > maxYear {
		return fmt.Errorf("%w: year %d out of [%d, %d]", ErrInvalidDate, year, minYear, maxYear)
	}
	if month < 1 || month > 12 {
		return fmt.Errorf("%w: month %d out of [1, 12]", ErrInvalidDate, month)
	}
	if day < 1 || day > daysInMonth(year, month) {
		return fmt.Errorf("%w: day %d out of range for %04d-%02d", ErrInvalidDate, day, year, month)
	}
	return nil
}

// weekday 返回 d 是星期几，0=周一 ... 6=周日。
// 以公元 1 年 1 月 1 日（周一）为基准累计天数推算。
func weekday(d int) int {
	year, month, day := splitDate(d)
	days := daysBeforeYear(year) + dayOfYear(year, month, day) - 1
	return days % 7
}

// daysBeforeYear 返回从公元 1 年到 year 年（不含）之前的总天数。
func daysBeforeYear(year int) int {
	y := year - 1
	return 365*y + y/4 - y/100 + y/400
}

// dayOfYear 返回日期在当年中的序数（1 月 1 日为 1）。
func dayOfYear(year, month, day int) int {
	n := day
	for m := 1; m < month; m++ {
		n += daysInMonth(year, m)
	}
	return n
}

// shiftDay 把 d 向前（delta=1）或向后（delta=-1）移动一天。
// 调用方需保证移动后年份仍在 [minYear, maxYear] 内。
func shiftDay(d, delta int) int {
	year, month, day := splitDate(d)
	day += delta
	if day < 1 {
		month--
		if month < 1 {
			month = 12
			year--
		}
		day = daysInMonth(year, month)
	} else if day > daysInMonth(year, month) {
		day = 1
		month++
		if month > 12 {
			month = 1
			year++
		}
	}
	return year*10000 + month*100 + day
}
