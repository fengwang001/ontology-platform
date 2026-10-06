package settlement

import (
	"fmt"
	"regexp"
	"time"
)

// tz 是判定日界、月界与日类型的固定时区（UTC+8，无夏令时，保证可复现）。
var tz = time.FixedZone("UTC+8", 8*3600)

// monthKey 月份键：year*12 + month - 1。
type monthKey int

func makeMonthKey(year, month int) monthKey { return monthKey(year*12 + month - 1) }

func (mk monthKey) year() int  { return int(mk) / 12 }
func (mk monthKey) month() int { return int(mk)%12 + 1 }

func (mk monthKey) String() string { return fmt.Sprintf("%04d-%02d", mk.year(), mk.month()) }

// start 返回该月第一天的起始时刻（含）。
func (mk monthKey) start() int64 {
	return time.Date(mk.year(), time.Month(mk.month()), 1, 0, 0, 0, 0, tz).Unix()
}

// end 返回该月结束时刻（不含），即次月起始时刻。
func (mk monthKey) end() int64 { return (mk + 1).start() }

// monthOf 返回时刻 t 所在的月份。
func monthOf(t int64) monthKey {
	tm := time.Unix(t, 0).In(tz)
	return makeMonthKey(tm.Year(), int(tm.Month()))
}

// dayStartOf 返回 t 所在日的起始时刻（当地零点）。
func dayStartOf(t int64) int64 {
	tm := time.Unix(t, 0).In(tz)
	return time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, tz).Unix()
}

// dateKey 日期键：yyyymmdd。
type dateKey int

func dateKeyOf(t int64) dateKey {
	tm := time.Unix(t, 0).In(tz)
	return dateKey(tm.Year()*10000 + int(tm.Month())*100 + tm.Day())
}

// calendar 维护节假日登记，并据此判定任意时刻的日类型。
type calendar struct {
	holidays map[dateKey]bool
}

func newCalendar() *calendar { return &calendar{holidays: map[dateKey]bool{}} }

// dayTypeAt 判定时刻 t 所在日的日类型：节假日 > 休息日 > 工作日。
// 日类型只决定使用哪张时段表，不改变时段边界的时刻值。
func (c *calendar) dayTypeAt(t int64) DayType {
	if c.holidays[dateKeyOf(t)] {
		return Holiday
	}
	switch time.Unix(t, 0).In(tz).Weekday() {
	case time.Saturday, time.Sunday:
		return Restday
	}
	return Workday
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// parseDate 解析 "2006-01-02" 格式的日期，返回日期键与当日起始时刻。
func parseDate(s string) (dateKey, int64, error) {
	if !dateRe.MatchString(s) {
		return 0, 0, fmt.Errorf("日期格式错: %q", s)
	}
	tm, err := time.ParseInLocation("2006-01-02", s, tz)
	if err != nil || tm.Format("2006-01-02") != s {
		return 0, 0, fmt.Errorf("日期格式错: %q", s)
	}
	ds := tm.Unix()
	if ds < MinTime || ds >= MaxTime {
		return 0, 0, fmt.Errorf("日期越界: %q", s)
	}
	return dateKeyOf(ds), ds, nil
}

// checkYearMonth 校验账期参数并返回月份键。
func checkYearMonth(year, month int) (monthKey, error) {
	if month < 1 || month > 12 {
		return 0, fmt.Errorf("月份越界: %d-%02d", year, month)
	}
	mk := makeMonthKey(year, month)
	if mk.start() < MinTime || mk.start() >= MaxTime {
		return 0, fmt.Errorf("月份越界: %d-%02d", year, month)
	}
	return mk, nil
}
