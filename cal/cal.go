// Package cal 提供公历基础：闰年、月日合法性、年天数、星期几。
package cal

import "errors"

// ErrDate 表示非法的公历日期（如 2 月 30 日、13 月）。
var ErrDate = errors.New("cal: invalid calendar date")

// IsLeap 判断 y 是否闰年。
func IsLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

// DaysInYear 返回一年的天数。
func DaysInYear(y int) int {
	if IsLeap(y) {
		return 366
	}
	return 365
}

// DaysInMonth 返回 y 年 m 月的天数，m 越界返回 0。
func DaysInMonth(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if IsLeap(y) {
			return 29
		}
		return 28
	}
	return 0
}

// Valid 判断 y-m-d 是否合法公历日期。
func Valid(y, m, d int) bool { return m >= 1 && m <= 12 && d >= 1 && d <= DaysInMonth(y, m) }

// Days 返回 y-m-d 距 1970-01-01 的天数（Howard Hinnant 算法，直接推算）。
func Days(y, m, d int) int {
	if m <= 2 {
		y--
	}
	era := divFloor(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	return era*146097 + yoe*365 + yoe/4 - yoe/100 + doy - 719468
}

// FromDays 是 Days 的逆运算，把距 1970-01-01 的天数还原为 y-m-d。
func FromDays(z int) (y, m, d int) {
	z += 719468
	era := divFloor(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
		y++
	}
	return y, m, d
}

// Weekday 返回 y-m-d 是星期几：1=周一 … 7=周日。1970-01-01 是周四。
func Weekday(y, m, d int) int {
	w := (Days(y, m, d) + 3) % 7
	if w < 0 {
		w += 7
	}
	return w + 1
}

// DayOfYear 返回 y-m-d 是当年第几天。
func DayOfYear(y, m, d int) int {
	n := d
	for i := 1; i < m; i++ {
		n += DaysInMonth(y, i)
	}
	return n
}

func divFloor(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
