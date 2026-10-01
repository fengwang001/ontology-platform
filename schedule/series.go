package schedule

// Rule 描述「每隔 Interval 个月，取该月第 Nth 个星期 Weekday」。
type Rule struct {
	Interval int
	Nth      int // 1..5，-1 表示最后一个
	Weekday  int // ISO 1..7
}

func (r Rule) validate() error {
	if r.Interval < 1 {
		return ErrInvalidInterval
	}
	if r.Nth != -1 && (r.Nth < 1 || r.Nth > 5) {
		return ErrInvalidNth
	}
	if r.Weekday < 1 || r.Weekday > 7 {
		return ErrInvalidWeekday
	}
	return nil
}

// nthWeekdayOfMonth 返回 (y, m) 月第 nth 个星期 w 的日期；
// nth 为 -1 时表示最后一个。该月不存在（仅 nth=5 可能）时 ok=false。
func nthWeekdayOfMonth(y, m, nth, w int) (Date, bool) {
	first := Date{y, m, 1}
	offset := (w - first.weekday() + 7) % 7
	dim := daysInMonth(y, m)
	var day int
	if nth == -1 {
		day = 1 + offset + 7*((dim-1-offset)/7)
	} else {
		day = 1 + offset + 7*(nth-1)
		if day > dim {
			return Date{}, false
		}
	}
	return Date{y, m, day}, true
}

type exception struct {
	canceled bool
	movedTo  Date
}

type series struct {
	id       string
	start    Date
	rule     Rule
	hasCount bool
	count    int
	until    Date
	exc      map[Date]exception
}

// Instance 是展开结果中的一项。
type Instance struct {
	SeriesID string
	Original Date
	Actual   Date
}

// originals 按升序返回所有实例的原日期。
//
// start 所在月为第 0 个月，之后每隔 Interval 个月取候选；
// 第 5 个星期几不存在的月份跳过且不占名额；早于 start 的候选
// 不算实例；候选晚于 2200-12-31 时停止生成。count / until 均按
// 原日期计。
func (s *series) originals() []Date {
	var out []Date
	base := s.start.Year*12 + (s.start.Month - 1)
	for i := 0; ; i += s.rule.Interval {
		mi := base + i
		y, m := mi/12, mi%12+1
		if y > maxDate.Year {
			break
		}
		cand, ok := nthWeekdayOfMonth(y, m, s.rule.Nth, s.rule.Weekday)
		if !ok {
			continue
		}
		if less(cand, s.start) {
			continue
		}
		if less(maxDate, cand) {
			break
		}
		if !s.hasCount && less(s.until, cand) {
			break
		}
		out = append(out, cand)
		if s.hasCount && len(out) >= s.count {
			break
		}
	}
	return out
}

// expand 应用例外后返回实例（取消的不出现）。
func (s *series) expand() []Instance {
	var out []Instance
	for _, o := range s.originals() {
		if e, ok := s.exc[o]; ok {
			if e.canceled {
				continue
			}
			out = append(out, Instance{SeriesID: s.id, Original: o, Actual: e.movedTo})
			continue
		}
		out = append(out, Instance{SeriesID: s.id, Original: o, Actual: o})
	}
	return out
}
