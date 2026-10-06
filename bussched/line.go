package bussched

import "fmt"

// Line 为线路：顺序站点、控制站标记与运行方案，
// 并预计算计划到站偏移前缀和，使查询开销与上报数量无关。
type Line struct {
	stations    []string
	index       map[string]int
	control     []bool
	plan        Plan
	planArr     []int64 // planArr[i] = 从首站出发到第 i 站到站的计划偏移
	dwellPrefix []int64 // dwellPrefix[i] = 前 i 站停站时长之和
}

// NewLine 校验方案并构建线路。所有时长必须为正整数秒。
func NewLine(stations []string, control []string, p Plan) (*Line, error) {
	n := len(stations)
	if n < 2 {
		return nil, newErr(ErrInvalidParam, "线路至少需要两个站点")
	}
	idx := make(map[string]int, n)
	for i, s := range stations {
		if s == "" {
			return nil, newErr(ErrInvalidParam, "站点名不能为空")
		}
		if _, dup := idx[s]; dup {
			return nil, newErr(ErrInvalidParam, fmt.Sprintf("站点 %q 重复", s))
		}
		idx[s] = i
	}
	ctrl := make([]bool, n)
	for _, c := range control {
		i, ok := idx[c]
		if !ok {
			return nil, newErr(ErrInvalidParam, fmt.Sprintf("控制站 %q 不在线路中", c))
		}
		ctrl[i] = true
	}
	if p.Headway <= 0 || p.HoldCap <= 0 || p.Tolerance <= 0 || p.MaxOnDuty <= 0 {
		return nil, newErr(ErrInvalidParam, "发车间隔/扣车上限/容忍量/工时上限必须为正整数秒")
	}
	if len(p.Dwell) != n {
		return nil, newErr(ErrInvalidParam, "停站时长数量须等于站数")
	}
	if len(p.Travel) != n-1 {
		return nil, newErr(ErrInvalidParam, "行驶时长数量须等于站数减一")
	}
	for i, d := range p.Dwell {
		if d <= 0 {
			return nil, newErr(ErrInvalidParam, fmt.Sprintf("第 %d 站停站时长必须为正", i))
		}
	}
	for i, t := range p.Travel {
		if t <= 0 {
			return nil, newErr(ErrInvalidParam, fmt.Sprintf("第 %d 段行驶时长必须为正", i))
		}
	}
	l := &Line{
		stations:    append([]string(nil), stations...),
		index:       idx,
		control:     ctrl,
		plan:        p,
		planArr:     make([]int64, n),
		dwellPrefix: make([]int64, n+1),
	}
	for i := 1; i < n; i++ {
		l.planArr[i] = l.planArr[i-1] + p.Dwell[i-1] + p.Travel[i-1]
	}
	for i := 0; i < n; i++ {
		l.dwellPrefix[i+1] = l.dwellPrefix[i] + p.Dwell[i]
	}
	return l, nil
}

func (l *Line) numStations() int { return len(l.stations) }

func (l *Line) stationName(i int) string { return l.stations[i] }

// nextControl 返回 idx 之后第一个控制站下标；没有则返回站数。
func (l *Line) nextControl(idx int) int {
	for i := idx + 1; i < len(l.stations); i++ {
		if l.control[i] {
			return i
		}
	}
	return len(l.stations)
}
