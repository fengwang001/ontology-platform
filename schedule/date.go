package schedule

import (
	"strconv"
	"strings"
)

const (
	minYear = 1970
	maxYear = 2200
)

// Date 是一个已校验的公历日期（1970-01-01 至 2200-12-31）。
// ord 为相对 1970-01-01 的序日，1970-01-01（周四）的 ord 为 0。
type Date struct {
	year, month, day int
	ord              int
}

var (
	minDate = Date{year: minYear, month: 1, day: 1, ord: 0}
	maxDate = mustDate(maxYear, 12, 31)
)

// ordTable 预计算 1970-01-01 .. 2200-12-31 全部日期（约 8.4 万个），
// 使 dateFromOrd 成为 O(1)，供朴素逐日扫描与展开使用。
var ordTable []Date

func init() {
	ordTable = make([]Date, 0, maxDate.ord+1)
	for year := minYear; year <= maxYear; year++ {
		for month := 1; month <= 12; month++ {
			for day := 1; day <= daysInMonth(year, month); day++ {
				ordTable = append(ordTable, Date{
					year: year, month: month, day: day,
					ord: len(ordTable),
				})
			}
		}
	}
}

// ParseDate 解析 "YYYY-MM-DD"，拒绝非法格式、不存在的日期（如 2 月 30 日）
// 以及超出 [1970-01-01, 2200-12-31] 的日期。
func ParseDate(s string) (Date, error) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return Date{}, opError(ErrInvalidDate, "date must be YYYY-MM-DD: "+s)
	}
	year, err1 := strconv.Atoi(s[0:4])
	month, err2 := strconv.Atoi(s[5:7])
	day, err3 := strconv.Atoi(s[8:10])
	if err1 != nil || err2 != nil || err3 != nil {
		return Date{}, opError(ErrInvalidDate, "date must be YYYY-MM-DD: "+s)
	}
	d, ok := makeDate(year, month, day)
	if !ok {
		return Date{}, opError(ErrInvalidDate, "date does not exist or out of range: "+s)
	}
	return d, nil
}

func mustDate(year, month, day int) Date {
	d, ok := makeDate(year, month, day)
	if !ok {
		panic("invalid date")
	}
	return d
}

func makeDate(year, month, day int) (Date, bool) {
	if year < minYear || year > maxYear || month < 1 || month > 12 || day < 1 {
		return Date{}, false
	}
	if day > daysInMonth(year, month) {
		return Date{}, false
	}
	return Date{year: year, month: month, day: day, ord: daysBeforeYear(year) + daysBeforeMonth(year, month) + day - 1}, true
}

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(year) {
			return 29
		}
		return 28
	default:
		return 0
	}
}

func daysBeforeMonth(year, month int) int {
	days := 0
	for m := 1; m < month; m++ {
		days += daysInMonth(year, m)
	}
	return days
}

func daysBeforeYear(year int) int {
	y := year - 1
	return 365*y + y/4 - y/100 + y/400 - epochDaysBefore1970
}

// 公历 0001-01-01 至 1970-01-01 之间的天数。
const epochDaysBefore1970 = 719162

func dateFromOrd(ord int) (Date, bool) {
	if ord < minDate.ord || ord > maxDate.ord {
		return Date{}, false
	}
	return ordTable[ord], true
}

// String 返回规范化的 "YYYY-MM-DD"。
func (d Date) String() string {
	var b strings.Builder
	b.Grow(10)
	write4 := func(v int) {
		b.WriteByte(byte('0' + (v/1000)%10))
		b.WriteByte(byte('0' + (v/100)%10))
		b.WriteByte(byte('0' + (v/10)%10))
		b.WriteByte(byte('0' + v%10))
	}
	write2 := func(v int) {
		b.WriteByte(byte('0' + v/10))
		b.WriteByte(byte('0' + v%10))
	}
	write4(d.year)
	b.WriteByte('-')
	write2(d.month)
	b.WriteByte('-')
	write2(d.day)
	return b.String()
}

// Before 报告 d 是否严格早于 other。
func (d Date) Before(other Date) bool { return d.ord < other.ord }

// Weekday 返回 ISO 星期：周一=1 … 周日=7。
// 1970-01-01 为周四：以周日=0 计 g=(ord+4)%7，转为 ISO 为 (g+6)%7+1。
func (d Date) Weekday() int { return ((d.ord+4)%7+6)%7 + 1 }

// Equal 报告两个日期是否相同。
func (d Date) Equal(other Date) bool { return d.ord == other.ord }

// addDays 返回 d 之后 delta 天的日期，越出合法范围时 ok=false。
func (d Date) addDays(delta int) (Date, bool) {
	return dateFromOrd(d.ord + delta)
}
