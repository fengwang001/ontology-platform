package ontology

import "time"

// TimeGrid 定义全系统统一的固定计量间隔，并负责间隔起点对齐校验与月份算术。
type TimeGrid struct {
	intervalSeconds int64
}

// NewTimeGrid 创建计量间隔网格。间隔必须为正数，且能整除一小时（保证每个自然月完整覆盖）。
func NewTimeGrid(interval time.Duration) (*TimeGrid, error) {
	secs := int64(interval / time.Second)
	if secs <= 0 || int64(time.Hour/time.Second)%secs != 0 {
		return nil, illegal("interval must be a positive divisor of one hour")
	}
	return &TimeGrid{intervalSeconds: secs}, nil
}

// AlignedStart 报告 t 是否对齐计量间隔起点（以 UTC Unix 时间为基准）。
func (g *TimeGrid) AlignedStart(t time.Time) bool {
	return t.UTC().Unix()%g.intervalSeconds == 0
}

// IntervalSeconds 返回间隔秒数。
func (g *TimeGrid) IntervalSeconds() int64 {
	return g.intervalSeconds
}

// MonthKey 是 UTC 自然月标识：年份*12 + 月份(1-12)。
type MonthKey int64

// MonthOf 返回给定时间所在的自然月。
func MonthOf(t time.Time) MonthKey {
	t = t.UTC()
	return MonthKey(int64(t.Year())*12 + int64(t.Month()) - 1)
}

// ParseMonth 解析 YYYY-MM 形式的月份字符串。
func ParseMonth(s string) (MonthKey, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return 0, illegal("month must be in YYYY-MM format: %q", s)
	}
	return MonthOf(t), nil
}

// String 返回 YYYY-MM。
func (m MonthKey) String() string {
	y := int64(m) / 12
	mo := int64(m)%12 + 1
	return time.Date(int(y), time.Month(mo), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
}

// Add 返回 m 之后 n 个月（n 可为负）。
func (m MonthKey) Add(n int64) MonthKey {
	return m + MonthKey(n)
}

// IntervalStarts 枚举属于月份 m 的全部计量间隔起点（按时间顺序）。
func (g *TimeGrid) IntervalStarts(m MonthKey) []time.Time {
	y := int(int64(m) / 12)
	mo := time.Month(int64(m)%12 + 1)
	start := time.Date(y, mo, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	secs := g.intervalSeconds
	n := int(end.Sub(start) / (time.Duration(secs) * time.Second))
	out := make([]time.Time, 0, n)
	for t := start; t.Before(end); t = t.Add(time.Duration(secs) * time.Second) {
		out = append(out, t)
	}
	return out
}
