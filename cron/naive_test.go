package cron

// naiveMatches 严格按规格逐分钟判定：月份满足后，
// 日与周都非通配取“或”，否则取“与”。
func naiveMatches(s *cronSpec, t int64) bool {
	_, mo, d, h, mi, err := FromMinute(t)
	if err != nil {
		return false
	}
	if !s.fields[fieldMonth].values[mo] {
		return false
	}
	if !s.fields[fieldHour].values[h] || !s.fields[fieldMinute].values[mi] {
		return false
	}
	w := int((t/minutes + 6) % 7)
	dom := s.fields[fieldDay].values[d]
	dow := s.fields[fieldWeek].values[w]
	if !s.fields[fieldDay].wildcard && !s.fields[fieldWeek].wildcard {
		return dom || dow
	}
	return dom && dow
}

// naiveNextFire 逐分钟扫描，作为 NextFire 的判定基准。
func naiveNextFire(spec string, t int64, horizon int64) (int64, bool) {
	s, err := Parse(spec)
	if err != nil {
		return 0, false
	}
	limit := t + horizon
	if limit > maxMinute {
		limit = maxMinute
	}
	for x := t + 1; x <= limit; x++ {
		if naiveMatches(s, x) {
			return x, true
		}
	}
	return 0, false
}
