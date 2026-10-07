// Package slotcoord 实现机场起降时刻协调与历史优先权系统。
//
// 系统按航季运转：航空公司在申请截止时刻前提交成系列的起降时刻申请，
// 截止时一次性结算分配（历史优先权 -> 新进入者保留额 -> 其余申请），
// 未被满足的申请进入对应小时段的等候名单。航季内支持按周返还、
// 系列交换、执行登记与豁免登记；航季结束时按使用率结算下一航季的
// 历史资格。所有变更操作携带当前时刻，单互斥锁保证并发调用等价于
// 某个串行顺序；相同输入序列重放得到逐比特相同的结果。
package slotcoord

import "errors"

// 统一拒绝次序：参数非法 > 时钟回退 > 公司或系列不存在 > 航季阶段不符
// > 登记超期 > 重复登记 > 容量不足。只报最靠前的一类。
var (
	ErrParam                 = errors.New("参数非法")
	ErrClockRegression       = errors.New("时钟回退")
	ErrNotFound              = errors.New("公司或系列不存在")
	ErrApplicationClosed     = errors.New("申请已截止")
	ErrSeasonSettled         = errors.New("航季已结算")
	ErrWeekExecuted          = errors.New("周已执行不可交换")
	ErrRegistrationLate      = errors.New("登记超期")
	ErrDuplicateRegistration = errors.New("重复登记")
	ErrCapacity              = errors.New("容量不足")
)

// errRank 返回错误在统一拒绝次序中的位次，越小越靠前。
func errRank(err error) int {
	switch {
	case errors.Is(err, ErrParam):
		return 0
	case errors.Is(err, ErrClockRegression):
		return 1
	case errors.Is(err, ErrNotFound):
		return 2
	case errors.Is(err, ErrApplicationClosed),
		errors.Is(err, ErrSeasonSettled),
		errors.Is(err, ErrWeekExecuted):
		return 3
	case errors.Is(err, ErrRegistrationLate):
		return 4
	case errors.Is(err, ErrDuplicateRegistration):
		return 5
	case errors.Is(err, ErrCapacity):
		return 6
	default:
		return -1
	}
}

// firstError 在候选错误中按统一拒绝次序返回最靠前的一个；全为 nil 时返回 nil。
func firstError(candidates ...error) error {
	bestRank := -1
	var best error
	for _, err := range candidates {
		if err == nil {
			continue
		}
		rank := errRank(err)
		if rank < 0 {
			return err
		}
		if best == nil || rank < bestRank {
			bestRank, best = rank, err
		}
	}
	return best
}

// Config 是系统配置。时刻为不透明整数刻度，由调用方赋予含义。
type Config struct {
	Weeks                int   // 航季总周数
	MinSeriesWeeks       int   // 系列最少周数
	Capacity             int   // 每个单元格(某周某天某小时段)容量上限
	HistoricThresholdPct int   // 历史优先权达标比例(百分数, 恰等于视为达标)
	NewcomerThreshold    int   // 新进入者阈值: 持有系列数少于此值即为新进入者
	SeasonStart          int64 // 第 0 周开始时刻
	WeekLength           int64 // 一周的时刻长度
	ApplicationDeadline  int64 // 申请截止时刻(含)
	ReturnDeadline       int64 // 返还截止时刻(含), 此前返还的周不计入分母
	RegistrationWindow   int64 // 周结束后登记窗口长度, 窗口终点取闭
}

func (c Config) validate() error {
	switch {
	case c.Weeks < 1,
		c.MinSeriesWeeks < 1,
		c.MinSeriesWeeks > c.Weeks,
		c.Capacity < 1,
		c.HistoricThresholdPct < 0,
		c.HistoricThresholdPct > 100,
		c.NewcomerThreshold < 1,
		c.WeekLength < 1,
		c.RegistrationWindow < 0:
		return ErrParam
	}
	return nil
}

// weekEnd 返回第 week 周的结束时刻。
func (c Config) weekEnd(week int) int64 {
	return c.SeasonStart + int64(week+1)*c.WeekLength
}

// seasonEnd 返回航季结束时刻。
func (c Config) seasonEnd() int64 {
	return c.weekEnd(c.Weeks - 1)
}

// Slot 是星期几与小时段的组合。
type Slot struct {
	Weekday int // [0,7)
	Hour    int // [0,24)
}

// Cell 是容量计数单元：某周某天某小时段。
type Cell struct {
	Week int
	Slot
}

// Request 是一个时刻系列申请。
type Request struct {
	ID          int // 系统按提交次序分配
	Airline     string
	Weekday     int
	Hour        int
	StartWeek   int
	EndWeek     int // 闭区间
	SubmittedAt int64
}

func (r Request) slot() Slot { return Slot{Weekday: r.Weekday, Hour: r.Hour} }

func (r Request) weekCount() int { return r.EndWeek - r.StartWeek + 1 }

// cells 返回申请覆盖的全部单元格。
func (r Request) cells() []Cell {
	cells := make([]Cell, 0, r.weekCount())
	for week := r.StartWeek; week <= r.EndWeek; week++ {
		cells = append(cells, Cell{Week: week, Slot: r.slot()})
	}
	return cells
}

func (r Request) validate(cfg Config) error {
	switch {
	case r.Airline == "",
		r.Weekday < 0, r.Weekday >= 7,
		r.Hour < 0, r.Hour >= 24,
		r.StartWeek < 0, r.EndWeek >= cfg.Weeks,
		r.StartWeek > r.EndWeek,
		r.weekCount() < cfg.MinSeriesWeeks:
		return ErrParam
	}
	return nil
}

// weekState 记录系列中某一周在航季内的状态。
type weekState struct {
	returned       bool // 已返还
	beforeDeadline bool // 返还发生在返还截止时刻(含)前
	registered     bool // 已登记执行/未执行
	executed       bool // 登记为已执行
	exempt         bool // 被认可的豁免(不可抗力)
}

// Series 是一个已分配的时刻系列。
type Series struct {
	ID        int
	Airline   string // 原始申请公司
	Holder    string // 当前持有者(交换后可能与 Airline 不同)
	Weekday   int
	Hour      int
	StartWeek int
	EndWeek   int
	weeks     []weekState // 下标 i 对应第 StartWeek+i 周
}

func (s *Series) slot() Slot { return Slot{Weekday: s.Weekday, Hour: s.Hour} }

func (s *Series) week(week int) *weekState { return &s.weeks[week-s.StartWeek] }

// Eligibility 是下一航季的历史资格：某公司对同一星期几、同一小时段、
// 同等周数范围的申请享有历史优先权。
type Eligibility struct {
	Airline   string
	Weekday   int
	Hour      int
	StartWeek int
	EndWeek   int
}

func (e Eligibility) matches(r Request) bool {
	return e.Airline == r.Airline &&
		e.Weekday == r.Weekday &&
		e.Hour == r.Hour &&
		e.StartWeek == r.StartWeek &&
		e.EndWeek == r.EndWeek
}

// Stats 是可验证的性能计数器, 用于证明关键操作的开销与
// 机场内系列总数、航季总周数无关。
type Stats struct {
	WaitlistCellVisits int64 // 返还触发等候名单分配时访问的等候者/单元格次数
	UsageWeekScans     int64 // 使用率计算扫描的周数
}

// waitEntry 是等候名单中的一个条目。
type waitEntry struct {
	req     Request
	blocked int // 覆盖单元格中剩余容量为 0 的数量
}
