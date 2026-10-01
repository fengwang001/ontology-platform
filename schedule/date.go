package schedule

import "fmt"

// Date 为公历日期，年份限制在 [1970, 2200]。
type Date struct {
	Year  int
	Month int
	Day   int
}

var (
	minDate = Date{1970, 1, 1}
	maxDate = Date{2200, 12, 31}
)

// ParseDate 解析严格的 "YYYY-MM-DD"。
func ParseDate(s string) (Date, error) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return Date{}, ErrDateFormat
	}
	var nums [3]int
	for i, span := range [][2]int{{0, 4}, {5, 7}, {8, 10}} {
		n := 0
		for j := span[0]; j < span[1]; j++ {
			if s[j] < '0' || s[j] > '9' {
				return Date{}, ErrDateFormat
			}
			n = n*10 + int(s[j]-'0')
		}
		nums[i] = n
	}
	d := Date{nums[0], nums[1], nums[2]}
	if d.Year < minDate.Year || d.Year > maxDate.Year {
		return Date{}, ErrDateRange
	}
	if d.Month < 1 || d.Month > 12 || d.Day < 1 || d.Day > daysInMonth(d.Year, d.Month) {
		return Date{}, ErrDateInvalid
	}
	return d, nil
}

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

func less(a, b Date) bool {
	if a.Year != b.Year {
		return a.Year < b.Year
	}
	if a.Month != b.Month {
		return a.Month < b.Month
	}
	return a.Day < b.Day
}

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

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

// daysFromCivil 与 civilFromDays 为 Howard Hinnant 的民用日期算法，
// 以 1970-01-01 为第 0 天的线性日计数，用于日期算术。
func daysFromCivil(y, m, d int) int {
	if m <= 2 {
		y--
	}
	era := y / 400
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func civilFromDays(z int) Date {
	z += 719468
	era := z / 146097
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp - 9
	if mp < 10 {
		m = mp + 3
	}
	if m <= 2 {
		y++
	}
	return Date{y, m, d}
}

func (d Date) addDays(n int) Date {
	return civilFromDays(daysFromCivil(d.Year, d.Month, d.Day) + n)
}

func (d Date) dayCount() int {
	return daysFromCivil(d.Year, d.Month, d.Day)
}

// weekday 返回 ISO 星期：周一 1 .. 周日 7。
func (d Date) weekday() int {
	// 1970-01-01 是星期四（ISO 4）。
	diff := d.dayCount() - daysFromCivil(1970, 1, 1)
	return ((3+diff)%7+7)%7 + 1
}
