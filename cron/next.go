package cron

// dayStepMax 是单次 NextFire 允许推进的最大天数（含起点天）。
// 4000 天（约 10.96 年）搜索窗口加上起点当天，即不超过 4001 步。
const dayStepMax = 4001

// 计数器仅用于测试断言：证明按天推进步数不超过 4001，而非逐分钟扫描。
var lastDaySteps int

// timeOfDay 是一个日内触发时刻（分钟，0..1439）。
type timeOfDay struct {
	hour   int
	minute int
}

func buildTimesOfDay(s *cronSpec) []timeOfDay {
	var times []timeOfDay
	for h := 0; h < 24; h++ {
		if !s.fields[fieldHour].values[h] {
			continue
		}
		for m := 0; m < 60; m++ {
			if s.fields[fieldMinute].values[m] {
				times = append(times, timeOfDay{hour: h, minute: m})
			}
		}
	}
	return times
}

// nextTimeOfDay 返回严格大于 curMinute 的最小日内分钟；没有时返回 -1。
// 直接在分、时集合中跳转，不逐分钟扫描。
func nextTimeOfDay(times []timeOfDay, curMinute int) int {
	lo, hi := 0, len(times)
	for lo < hi {
		mid := (lo + hi) / 2
		v := times[mid].hour*60 + times[mid].minute
		if v > curMinute {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == len(times) {
		return -1
	}
	return times[lo].hour*60 + times[lo].minute
}

// dayMatches 按公历年月日与星期判定日期字段是否满足。
func dayMatches(s *cronSpec, year, month, day, weekday int) bool {
	if !s.fields[fieldMonth].values[month] {
		return false
	}
	dom := s.fields[fieldDay].values[day]
	dow := s.fields[fieldWeek].values[weekday]
	dowWild := s.fields[fieldWeek].wildcard
	domWild := s.fields[fieldDay].wildcard
	if !domWild && !dowWild {
		// 都为显式集合：满足任一即可（或）。
		return dom || dow
	}
	// 至少一个带通配：两个集合都须满足（与）；通配集合恒为真。
	return dom && dow
}

// NextFire 返回严格大于 t 的最小匹配分钟。
// t 之后 4000×1440 分钟内无匹配，或匹配超过 2199 年末，报 ErrNoNext。
func NextFire(spec string, t int64) (int64, error) {
	if t < 0 || t > maxMinute {
		return 0, ErrIllegalTime
	}
	parsed, err := Parse(spec)
	if err != nil {
		return 0, err
	}
	return nextFireParsed(parsed, t)
}

// nextFireParsed 在已解析表达式上求下一次触发。
func nextFireParsed(parsed *cronSpec, t int64) (int64, error) {
	times := buildTimesOfDay(parsed)
	if len(times) == 0 {
		return 0, ErrNoNext
	}

	lastDaySteps = 0
	limit := t + 4000*minutes

	curDay := t / minutes
	curTime := int(t % minutes)
	for {
		if lastDaySteps >= dayStepMax {
			return 0, ErrNoNext
		}
		lastDaySteps++
		candidate := curDay*minutes + int64(curTime)
		if candidate > limit {
			return 0, ErrNoNext
		}
		y, mo, d, _, _, derr := FromMinute(curDay * minutes)
		if derr != nil {
			return 0, ErrNoNext
		}
		weekday := int(curDay+6) % 7 // 2000-01-01 为星期六；0 为星期日
		if dayMatches(parsed, y, mo, d, weekday) {
			mt := nextTimeOfDay(times, curTime)
			if mt >= 0 {
				result := curDay*minutes + int64(mt)
				if result <= t || result > maxMinute || result > limit {
					return 0, ErrNoNext
				}
				return result, nil
			}
		}
		curDay++
		curTime = -1
	}
}
