// Package api 对外提供 ISO 8601 周日期换算与自检。
package api

import "ontology/isoweek"

// Date 是 ISO 周日期：周所属年 + 周序号 + 周内第几天（周一=1 … 周日=7）。
type Date = isoweek.Date

// ToISO 把公历日期 y-m-d 换算为 ISO 周日期；非法日期返回 cal.ErrDate。
func ToISO(y, m, d int) (Date, error) { return isoweek.ToISO(y, m, d) }

// FromISO 把 ISO 周日期反解为公历日期；周序号或周内天数越界返回
// isoweek.ErrWeek 或 isoweek.ErrWeekday。
func FromISO(year, week, day int) (y, m, d int, err error) { return isoweek.FromISO(year, week, day) }

// WeeksInYear 返回 ISO 年 y 的周数（52 或 53）。
func WeeksInYear(year int) int { return isoweek.WeeksInYear(year) }

// SelfCheck 对 [fromYear, toYear] 的每一天做 ToISO→FromISO 往返比对，
// 并核验周序号落在 WeeksInYear 给出的范围内；全部通过返回 nil。
func SelfCheck(fromYear, toYear int) error { return isoweek.SelfCheck(fromYear, toYear) }
