// Package isoweek 实现公历日期与 ISO 8601 周日期的双向换算。
package isoweek

import (
	"errors"
	"fmt"
	"ontology/cal"
	"sync/atomic"
)

// 哨兵错误：周序号越界与周内天数越界，互不相同，可用 errors.Is 判定。
var ErrWeek, ErrWeekday = errors.New("isoweek: week number out of range"), errors.New("isoweek: weekday out of range")

// Date 是 ISO 周日期：周所属年 + 周序号 + 周内第几天（周一=1 … 周日=7）。
type Date struct{ Year, Week, Day int }

// 非导出计数器：total 累计、last 记录 ToISO 最近一次调用星期几的次数；不进公开接口。
var weekdayCallsTotal, weekdayCallsLast atomic.Int64

func weekday(y, m, d int) int {
	weekdayCallsTotal.Add(1)
	return cal.Weekday(y, m, d)
}

// WeeksInYear 返回 ISO 年 y 的周数：53 ⟺ 1 月 1 日周四，或（闰年且 1 月 1 日周三）。
func WeeksInYear(y int) int {
	jan1 := weekday(y, 1, 1)
	if jan1 == 4 || (jan1 == 3 && cal.IsLeap(y)) {
		return 53
	}
	return 52
}

// ToISO 把公历日期换算为 ISO 周日期；非法日期整体失败，返回零值。
func ToISO(y, m, d int) (Date, error) {
	if !cal.Valid(y, m, d) {
		return Date{}, cal.ErrDate
	}
	start := weekdayCallsTotal.Load()
	defer func() { weekdayCallsLast.Store(weekdayCallsTotal.Load() - start) }()
	wd := weekday(y, m, d)
	week := (cal.DayOfYear(y, m, d) - wd + 10) / 7
	switch {
	case week < 1: // 年初几天属于上一年最后一周
		return Date{Year: y - 1, Week: WeeksInYear(y - 1), Day: wd}, nil
	case week > WeeksInYear(y): // 年末几天属于下一年第 1 周
		return Date{Year: y + 1, Week: 1, Day: wd}, nil
	}
	return Date{Year: y, Week: week, Day: wd}, nil
}

// FromISO 把 ISO 周日期反解为公历日期；越界输入整体失败，返回零值。
func FromISO(year, week, day int) (y, m, d int, err error) {
	if day < 1 || day > 7 {
		return 0, 0, 0, ErrWeekday
	}
	if week < 1 || week > WeeksInYear(year) {
		return 0, 0, 0, ErrWeek
	}
	jan4 := cal.Days(year, 1, 4) // 1 月 4 日必在第 1 周
	monday1 := jan4 - (cal.Weekday(year, 1, 4) - 1)
	y, m, d = cal.FromDays(monday1 + (week-1)*7 + (day - 1))
	return y, m, d, nil
}

// SelfCheck 对 [fromYear, toYear] 每天做 ToISO→FromISO 往返比对并核验周序号范围。
func SelfCheck(fromYear, toYear int) error {
	for y := fromYear; y <= toYear; y++ {
		for z := cal.Days(y, 1, 1); z <= cal.Days(y, 12, 31); z++ {
			cy, cm, cd := cal.FromDays(z)
			iso, err := ToISO(cy, cm, cd)
			if err != nil {
				return err
			}
			if iso.Week < 1 || iso.Week > WeeksInYear(iso.Year) {
				return fmt.Errorf("isoweek: week %d out of range for year %d", iso.Week, iso.Year)
			}
			ry, rm, rd, err := FromISO(iso.Year, iso.Week, iso.Day)
			if err != nil || ry != cy || rm != cm || rd != cd {
				return fmt.Errorf("isoweek: round trip failed for %d-%02d-%02d", cy, cm, cd)
			}
		}
	}
	return nil
}

// WeekdayBudgetOK 在 1900..2100 抽样 samples 个日期做 ToISO，报告每次调用的星期几计算次数是否都不超过 limit。
func WeekdayBudgetOK(samples, limit int) bool {
	lo, hi := cal.Days(1900, 1, 1), cal.Days(2100, 12, 31)
	for i := 0; i < samples; i++ {
		y, m, d := cal.FromDays(lo + (hi-lo)*i/samples)
		if _, err := ToISO(y, m, d); err != nil {
			return false
		}
		if weekdayCallsLast.Load() > int64(limit) {
			return false
		}
	}
	return true
}
