package leasechain

import "time"

// epoch 为日序号 0 对应的日历日。
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// dayToTime 将整数日序号转换为该日 0 点（UTC）。
func dayToTime(day int) time.Time {
	return epoch.AddDate(0, 0, day)
}

// addMonths 返回 base 推后 months 个月的日历日（按日序对齐，溢出则取当月最后一天）。
func addMonths(base time.Time, months int) time.Time {
	y, m := int(base.Year()), int(base.Month())
	total := y*12 + (m - 1) + months
	ny, nm := total/12, total%12+1
	d := base.Day()
	last := time.Date(ny, time.Month(nm), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(ny, time.Month(nm), d, 0, 0, 0, 0, time.UTC)
}

// dueOf 返回 year 年 month 月（month 从 1 起）的固定付款日。
func dueOf(year, month, payDay int) int {
	t := time.Date(year, time.Month(month), payDay, 0, 0, 0, 0, time.UTC)
	return int(t.Sub(epoch).Hours() / 24)
}

// installments 枚举一份租约 [start,end) 内全部账期的（应付日, 租金）。
// 首个账期的应付日不早于起始日；应付日不小于终止日的账期不计。
func installments(start, end, payDay int, rent int64) []bill {
	if end <= start || rent <= 0 {
		return nil
	}
	first := dayToTime(start)
	y, m := first.Year(), int(first.Month())
	var out []bill
	for {
		due := dueOf(y, m, payDay)
		if due < start {
			due = start // 起始日所在账期：应付日不早于起租日
		}
		if due >= end {
			break
		}
		out = append(out, bill{due: due, amount: rent})
		next := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
		y, m = next.Year(), int(next.Month())
	}
	return out
}

// isOverdue 判断应付日 due 的账期在 now 是否逾期达宽限 g 天。
func isOverdue(due, now, g int) bool {
	return now >= due+g
}
