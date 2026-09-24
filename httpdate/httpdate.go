// Package httpdate 严格解析并比较 IMF-fixdate 形式的 HTTP 时间戳：
// "Mon, 02 Jan 2006 15:04:05 GMT"。不依赖 time.Parse，不依赖其他包。
package httpdate

import (
	"errors"
	"time"
)

// 四类解析错误，彼此可用 errors.Is 区分。
var (
	ErrWhitespace = errors.New("httpdate: 布局或空白不符")   // 多余空白/分隔符错位
	ErrSuffix     = errors.New("httpdate: 缺少 GMT 后缀") // 非 GMT 后缀
	ErrMonth      = errors.New("httpdate: 非法月份名")     // 月份名非法
	ErrDate       = errors.New("httpdate: 日历日期不存在")   // 如 2 月 30 日
)

var months = map[string]time.Month{
	"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6,
	"Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12,
}

var weekdays = map[string]bool{
	"Mon": true, "Tue": true, "Wed": true, "Thu": true,
	"Fri": true, "Sat": true, "Sun": true,
}

// Parse 严格解析 IMF-fixdate。任何布局偏差都返回上述四类错误之一。
func Parse(s string) (time.Time, error) {
	if len(s) != 29 || s[3] != ',' || s[4] != ' ' || s[7] != ' ' ||
		s[11] != ' ' || s[16] != ' ' || s[25] != ' ' ||
		s[19] != ':' || s[22] != ':' {
		return time.Time{}, ErrWhitespace
	}
	if s[26:] != "GMT" {
		return time.Time{}, ErrSuffix
	}
	if !weekdays[s[0:3]] {
		return time.Time{}, ErrDate
	}
	month, ok := months[s[8:11]]
	if !ok {
		return time.Time{}, ErrMonth
	}
	day, ok1 := digits(s[5:7], 2)
	year, ok2 := digits(s[12:16], 4)
	hour, ok3 := digits(s[17:19], 2)
	min, ok4 := digits(s[20:22], 2)
	sec, ok5 := digits(s[23:25], 2)
	if !(ok1 && ok2 && ok3 && ok4 && ok5) {
		return time.Time{}, ErrDate
	}
	if day < 1 || day > daysIn(month, year) ||
		hour > 23 || min > 59 || sec > 59 {
		return time.Time{}, ErrDate
	}
	return time.Date(year, month, day, hour, min, sec, 0, time.UTC), nil
}

// Compare 比较两个时刻：a 早于/等于/晚于 b 时返回 -1/0/1。
func Compare(a, b time.Time) int {
	return a.Compare(b)
}

func digits(s string, n int) (int, bool) {
	v := 0
	for i := 0; i < n; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		v = v*10 + int(s[i]-'0')
	}
	return v, true
}

func daysIn(m time.Month, y int) int {
	switch m {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	}
	return 31
}
