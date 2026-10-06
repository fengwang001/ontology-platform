package slots

import (
	"errors"
	"sync"
)

type phase int

const (
	phaseRequests phase = iota // 申请阶段
	phaseInSeason              // 航季内（已分配未结算）
	phaseSettled               // 已结算
)

// Engine 时刻协调引擎。所有变更操作互斥串行化，等价于某个串行顺序。
type Engine struct {
	cfg Config

	mu       sync.Mutex
	lastTime int64
	clockSet bool
	phase    phase

	historic map[HistoricKey]bool // 上一航季结算出的历史资格

	requests      []*Request // 按提交次序
	nextRequestID int

	series       map[int]*Series
	seriesOrder  []int
	nextSeriesID int

	occ      map[CellKey]int        // 单元格已分配数，O(1) 查询
	trees    map[SlotKey]*segTree   // 每个小时段的区间最大值线段树，O(log W) 查询
	waitlist map[SlotKey][]*Request // 等候名单，按提交时刻排序

	eligibility []HistoricKey // 结算产物：下一航季历史资格清单

	stats Stats
}

// NewEngine 创建引擎；historics 为上一航季结算得到的历史资格清单。
func NewEngine(cfg Config, historics []HistoricKey) (*Engine, error) {
	if cfg.TotalWeeks < 1 || cfg.MinSeriesWeeks < 1 || cfg.CapacityPerSlot < 1 ||
		cfg.HistoricThresholdPercent < 0 || cfg.HistoricThresholdPercent > 100 ||
		cfg.NewcomerThreshold < 0 || cfg.WeekLength < 1 || cfg.RegistrationWindow < 0 {
		return nil, errors.New("slots: 非法配置")
	}
	e := &Engine{
		cfg:      cfg,
		historic: make(map[HistoricKey]bool, len(historics)),
		series:   map[int]*Series{},
		occ:      map[CellKey]int{},
		trees:    map[SlotKey]*segTree{},
		waitlist: map[SlotKey][]*Request{},
	}
	for _, h := range historics {
		e.historic[h] = true
	}
	return e, nil
}

func (e *Engine) checkClock(now int64) error {
	if e.clockSet && now < e.lastTime {
		return reject(ReasonClockRollback, "时刻 %d 早于上次被接受操作的时刻 %d", now, e.lastTime)
	}
	return nil
}

func (e *Engine) advance(now int64) {
	e.lastTime = now
	e.clockSet = true
}

func (e *Engine) validateSpec(spec RequestSpec) error {
	if spec.Airline == "" {
		return reject(ReasonInvalidParam, "公司名为空")
	}
	if spec.Weekday < 0 || spec.Weekday > 6 || spec.Hour < 0 || spec.Hour > 23 {
		return reject(ReasonInvalidParam, "星期几或小时段越界")
	}
	if spec.StartWeek < 0 || spec.EndWeek < spec.StartWeek || spec.EndWeek >= e.cfg.TotalWeeks {
		return reject(ReasonInvalidParam, "周范围越界")
	}
	if spec.EndWeek-spec.StartWeek+1 < e.cfg.MinSeriesWeeks {
		return reject(ReasonInvalidParam, "系列周数不足最少周数 %d", e.cfg.MinSeriesWeeks)
	}
	return nil
}

// Submit 提交航季申请，须在申请截止时刻之前且处于申请阶段。
func (e *Engine) Submit(spec RequestSpec, now int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.validateSpec(spec); err != nil {
		return -1, err
	}
	if err := e.checkClock(now); err != nil {
		return -1, err
	}
	if e.phase != phaseRequests || now >= e.cfg.RequestDeadline {
		return -1, reject(ReasonDeadlinePassed, "申请已截止")
	}
	req := &Request{
		ID: e.nextRequestID, Airline: spec.Airline,
		Weekday: spec.Weekday, Hour: spec.Hour,
		StartWeek: spec.StartWeek, EndWeek: spec.EndWeek,
		SubmittedAt: now,
		Historic:    e.historic[spec.historicKey()],
	}
	e.nextRequestID++
	e.requests = append(e.requests, req)
	e.advance(now)
	return req.ID, nil
}

// Allocate 在申请截止时刻（或之后）触发一次性结算分配。
func (e *Engine) Allocate(now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase == phaseRequests {
		// 历史申请之间超过容量属参数非法，先于时钟检查报告。
		if err := e.checkHistoricFeasible(); err != nil {
			return err
		}
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	switch e.phase {
	case phaseSettled:
		return reject(ReasonSeasonSettled, "航季已结算")
	case phaseInSeason:
		return reject(ReasonPhaseMismatch, "分配已执行过")
	}
	if now < e.cfg.RequestDeadline {
		return reject(ReasonPhaseMismatch, "申请截止时刻未到")
	}
	e.runAllocation()
	e.phase = phaseInSeason
	e.advance(now)
	return nil
}

// --- 只读快照（不推进时钟） ---

// Stats 返回性能计数器快照。
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// ResetStats 清零性能计数器。
func (e *Engine) ResetStats() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stats = Stats{}
}

// CellLoad 返回某单元格当前已分配数。
func (e *Engine) CellLoad(week, weekday, hour int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.occ[CellKey{Week: week, Weekday: weekday, Hour: hour}]
}

// SeriesSnapshot 按系列 ID 顺序返回全部系列的拷贝。
func (e *Engine) SeriesSnapshot() []Series {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Series, 0, len(e.seriesOrder))
	for _, id := range e.seriesOrder {
		s := *e.series[id]
		weeks := make(map[int]WeekState, len(s.Weeks))
		for w, st := range s.Weeks {
			weeks[w] = st
		}
		s.Weeks = weeks
		out = append(out, s)
	}
	return out
}

// WaitlistSnapshot 返回各小时段等候名单（申请 ID，按提交时刻排序）。
func (e *Engine) WaitlistSnapshot() map[SlotKey][]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[SlotKey][]int{}
	for k, list := range e.waitlist {
		ids := make([]int, 0, len(list))
		for _, r := range list {
			ids = append(ids, r.ID)
		}
		out[k] = ids
	}
	return out
}

// Eligibility 返回结算产生的下一航季历史资格清单。
func (e *Engine) Eligibility() []HistoricKey {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]HistoricKey, len(e.eligibility))
	copy(out, e.eligibility)
	return out
}
