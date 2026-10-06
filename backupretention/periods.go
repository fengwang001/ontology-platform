package backupretention

const secondsPerDay int64 = 86400

// periodOf 返回某时刻在指定层上的周期序号；同一周期内所有时刻序号相同，
// 序号随时间单调不减。时间单位为 UTC 秒。
func periodOf(layer Layer, t int64) int64 {
	switch layer {
	case LayerDaily:
		return dayPeriod(t)
	case LayerWeekly:
		return weekPeriod(t)
	case LayerMonthly:
		return monthPeriod(t)
	default:
		return -1
	}
}

// floorDiv 是向负无穷取整的整数除法；Go 的 / 对负数向零截断，这里显式校正。
func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// dayPeriod 以“自 1970-01-01 UTC 起的整日数”作为日周期序号。
func dayPeriod(t int64) int64 { return floorDiv(t, secondsPerDay) }

// civilFromDays 将“自纪元起的日数”换算为公历年月日（Howard Hinnant 算法，
// 对纪元前日期同样成立）。
func civilFromDays(z int64) (year, month, day int64) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day = doy - (153*mp+2)/5 + 1
	m := mp + 3 - 12*floorDiv(mp, 10)
	year = y + mp/10
	month = m
	return
}

// daysFromCivil 是 civilFromDays 的逆函数。
func daysFromCivil(year, month, day int64) int64 {
	y := year
	if month <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := month - 3
	if mp < 0 {
		mp += 12
	}
	doy := (153*mp+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// monthPeriod 以“连续公历月序号”作为月周期序号：年*12 + (月-1)。
// 月长不等（28/29/30/31）因此天然被正确处理。
func monthPeriod(t int64) int64 {
	d := dayPeriod(t)
	y, m, _ := civilFromDays(d)
	return y*12 + (m - 1)
}

// weekday0 返回星期序号：周一=0 … 周日=6。
// 1970-01-01 是周四（=3）。
func weekday0(d int64) int64 {
	w := (d + 3) % 7
	if w < 0 {
		w += 7
	}
	return w
}

// weekPeriod 以“周一开始的 UTC 周”的首日日期（自纪元起日数）作为周周期序号。
// 周因此可自然跨年、跨月。
func weekPeriod(t int64) int64 {
	d := dayPeriod(t)
	return d - weekday0(d)
}
