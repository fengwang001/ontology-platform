package schedule

import "fmt"

// validateRule 校验重复规则参数。
func validateRule(r Rule) *OpError {
	if r.K < 1 {
		return opError(ErrInvalidInterval, fmt.Sprintf("interval k must be >= 1, got %d", r.K))
	}
	if r.Nth < -1 || r.Nth == 0 || r.Nth > 5 {
		return opError(ErrInvalidNth, fmt.Sprintf("nth must be in [-1,1..5], got %d", r.Nth))
	}
	if r.W < 1 || r.W > 7 {
		return opError(ErrInvalidWeekday, fmt.Sprintf("weekday must be 1..7, got %d", r.W))
	}
	return nil
}

// nthWeekdayInMonth 返回 year/month 中第 nth 个星期 w 的日期；
// nth=-1 表示最后一个；不存在第 nth 个时 ok=false（月份被跳过，不顺延、不占名额）。
func nthWeekdayInMonth(year, month, nth, w int) (Date, bool) {
	first, ok := makeDate(year, month, 1)
	if !ok {
		return Date{}, false
	}
	firstW := first.Weekday()
	firstOcc := 1 + (w-firstW+7)%7
	var day int
	if nth == -1 {
		last := firstOcc + 28
		if last > daysInMonth(year, month) {
			last -= 7
		}
		day = last
	} else {
		day = firstOcc + 7*(nth-1)
		if day > daysInMonth(year, month) {
			return Date{}, false
		}
	}
	return makeDate(year, month, day)
}

// addMonthOffset 计算 base 的 (year,month) 再偏移 offset 个月后的年月（offset>=0）。
func addMonthOffset(year, month, offset int) (int, int) {
	total := (year)*12 + (month - 1) + offset
	return total / 12, total%12 + 1
}

// generateInstances 按规则生成从 start（第 0 个月）开始的候选实例原日期。
// 规则：
//   - 第 0 个月为 start 所在月，之后每隔 k 个月取该月第 nth 个星期 w；
//   - 早于 start 的候选不算实例（不顺延到下月）；
//   - 某月不存在第 5 个星期 w 时跳过该月，不顺延也不占名额；
//   - count 型生成 count 个即止；until 型生成到 until（含当日）；
//   - 候选晚于 2200-12-31 时停止；
//   - count 与 until 一律按原日期判定。
func generateInstances(start Date, r Rule, count int, untilOrd int, hasUntil bool) []Date {
	var out []Date
	if !hasUntil && count == 0 {
		return out
	}
	for offset := 0; ; offset += r.K {
		year, month := addMonthOffset(start.year, start.month, offset)
		if year > maxYear {
			break
		}
		cand, ok := nthWeekdayInMonth(year, month, r.Nth, r.W)
		if !ok {
			continue
		}
		if cand.ord > maxDate.ord {
			break
		}
		if cand.Before(start) {
			continue
		}
		if hasUntil && cand.ord > untilOrd {
			break
		}
		out = append(out, cand)
		if !hasUntil && len(out) >= count {
			break
		}
	}
	return out
}

// instancesBefore 返回 instances 中严格早于 date 的实例个数。
func instancesBefore(instances []Date, date Date) int {
	n := 0
	for _, d := range instances {
		if d.Before(date) {
			n++
		}
	}
	return n
}
