package backup

import "time"

// secondsPerDay 是一整天的秒数；时间一律按 UTC。
const secondsPerDay int64 = 86400

// dayIndex 返回时刻 t 所在的整日周期序号（从 Unix 纪元起第几天）。
func dayIndex(t int64) int64 {
	return t / secondsPerDay
}

// weekIndex 返回时刻 t 所在的周周期序号，一周从周一开始。
// Unix 纪元 1970-01-01 是周四，故日序号加 3 后每 7 天为一个周周期。
func weekIndex(t int64) int64 {
	return (dayIndex(t) + 3) / 7
}

// monthIndex 返回时刻 t 所在的公历月周期序号（year*12 + month-1）。
func monthIndex(t int64) int64 {
	y, m, _ := time.Unix(t, 0).UTC().Date()
	return int64(y)*12 + int64(m) - 1
}
