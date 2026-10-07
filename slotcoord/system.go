package slotcoord

import (
	"sort"
	"sync"
)

// System 是机场起降时刻协调系统。所有变更操作可并发调用，
// 单互斥锁保证结果等价于某个串行顺序。
type System struct {
	mu      sync.Mutex
	cfg     Config
	last    int64
	hasLast bool

	historic map[Eligibility]bool // 上一航季结算出的历史资格

	requests []Request       // 本航季全部申请, 按提交次序
	series   map[int]*Series // 系列 ID -> 系列
	nextID   int

	remaining map[Cell]int // 单元格剩余容量(缺省为 Capacity)
	reserve   map[Cell]int // 新进入者保留额, 仅分配阶段使用

	waitOrder   map[Slot][]int        // 小时段 -> 等候申请 ID, 按提交时刻排序
	waitEntry   map[int]*waitEntry    // 申请 ID -> 等候条目
	cellWaiters map[Cell]map[int]bool // 单元格 -> 覆盖它的等候申请 ID

	closed      bool          // 申请已截止并完成一次性分配
	settled     bool          // 航季已结算
	eligibility []Eligibility // 本航季结算产出(下一航季历史资格)

	stats  Stats
	logger func(format string, args ...any)
}

// NewSystem 创建系统, historic 为上一航季结算出的历史资格清单。
func NewSystem(cfg Config, historic []Eligibility) (*System, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &System{
		cfg:         cfg,
		historic:    make(map[Eligibility]bool, len(historic)),
		series:      map[int]*Series{},
		remaining:   map[Cell]int{},
		waitOrder:   map[Slot][]int{},
		waitEntry:   map[int]*waitEntry{},
		cellWaiters: map[Cell]map[int]bool{},
	}
	for _, e := range historic {
		s.historic[e] = true
	}
	return s, nil
}

// SetLogger 设置判定依据日志钩子(在锁内调用, 实现不得再调用 System 方法)。
func (s *System) SetLogger(fn func(format string, args ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = fn
}

func (s *System) log(format string, args ...any) {
	if s.logger != nil {
		s.logger(format, args...)
	}
}

// clockErr 检查时钟回退。
func (s *System) clockErr(now int64) error {
	if s.hasLast && now < s.last {
		return ErrClockRegression
	}
	return nil
}

// accept 在接受一个变更操作后推进时钟。
func (s *System) accept(now int64) {
	s.last = now
	s.hasLast = true
}

// remainingOf 返回单元格剩余容量, O(1), 与系列总数和航季周数无关。
func (s *System) remainingOf(c Cell) int {
	if v, ok := s.remaining[c]; ok {
		return v
	}
	return s.cfg.Capacity
}

func (s *System) isHistoric(r Request) bool {
	return s.historic[Eligibility{
		Airline:   r.Airline,
		Weekday:   r.Weekday,
		Hour:      r.Hour,
		StartWeek: r.StartWeek,
		EndWeek:   r.EndWeek,
	}]
}

// SubmitRequest 在申请截止时刻(含)前提交一个系列申请, 返回申请 ID。
func (s *System) SubmitRequest(now int64, req Request) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paramErr := req.validate(s.cfg)
	var phaseErr error
	switch {
	case s.settled:
		phaseErr = ErrSeasonSettled
	case s.closed || now > s.cfg.ApplicationDeadline:
		phaseErr = ErrApplicationClosed
	}
	if err := firstError(paramErr, s.clockErr(now), phaseErr); err != nil {
		return 0, err
	}
	s.accept(now)
	req.ID = len(s.requests)
	req.SubmittedAt = now
	s.requests = append(s.requests, req)
	s.log("申请 %d 提交: 公司=%s 时段=%+v 周=[%d,%d]", req.ID, req.Airline, req.slot(), req.StartWeek, req.EndWeek)
	return req.ID, nil
}

// Stats 返回性能计数器快照。
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Remaining 返回某周某天某小时段的剩余容量, O(1)。
func (s *System) Remaining(week, weekday, hour int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remainingOf(Cell{Week: week, Slot: Slot{Weekday: weekday, Hour: hour}})
}

// UsageOf 计算单个系列的使用率分子与分母, 开销仅随该系列周数增长。
func (s *System) UsageOf(seriesID int) (executed, planned int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ser, ok := s.series[seriesID]
	if !ok {
		return 0, 0, ErrNotFound
	}
	executed, planned = s.usage(ser)
	return executed, planned, nil
}

// Eligibility 返回本航季结算产出的下一航季历史资格清单。
func (s *System) Eligibility() []Eligibility {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Eligibility(nil), s.eligibility...)
}

// WeekView 是系列某一周状态的外部视图。
type WeekView struct {
	Week           int
	Returned       bool
	BeforeDeadline bool
	Registered     bool
	Executed       bool
	Exempt         bool
}

// SeriesView 是系列的外部视图。
type SeriesView struct {
	ID        int
	Airline   string
	Holder    string
	Weekday   int
	Hour      int
	StartWeek int
	EndWeek   int
	Weeks     []WeekView
}

// WaitlistView 是一个小时段的等候名单视图, 按提交时刻排序。
type WaitlistView struct {
	Slot     Slot
	Requests []int
}

// Snapshot 是系统状态的确定性快照, 用于可复现性验证。
type Snapshot struct {
	Closed      bool
	Settled     bool
	Series      []SeriesView
	Waitlists   []WaitlistView
	Eligibility []Eligibility
}

// Snapshot 返回当前状态快照, 序列内按 ID、等候名单按小时段、
// 资格清单按字典序排序, 保证相同状态产生逐比特相同的快照。
func (s *System) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := Snapshot{Closed: s.closed, Settled: s.settled}

	ids := make([]int, 0, len(s.series))
	for id := range s.series {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		ser := s.series[id]
		view := SeriesView{
			ID: ser.ID, Airline: ser.Airline, Holder: ser.Holder,
			Weekday: ser.Weekday, Hour: ser.Hour,
			StartWeek: ser.StartWeek, EndWeek: ser.EndWeek,
		}
		for i, w := range ser.weeks {
			view.Weeks = append(view.Weeks, WeekView{
				Week:           ser.StartWeek + i,
				Returned:       w.returned,
				BeforeDeadline: w.beforeDeadline,
				Registered:     w.registered,
				Executed:       w.executed,
				Exempt:         w.exempt,
			})
		}
		snap.Series = append(snap.Series, view)
	}

	slots := make([]Slot, 0, len(s.waitOrder))
	for slot := range s.waitOrder {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].Weekday != slots[j].Weekday {
			return slots[i].Weekday < slots[j].Weekday
		}
		return slots[i].Hour < slots[j].Hour
	})
	for _, slot := range slots {
		snap.Waitlists = append(snap.Waitlists, WaitlistView{
			Slot:     slot,
			Requests: append([]int(nil), s.waitOrder[slot]...),
		})
	}

	snap.Eligibility = append([]Eligibility(nil), s.eligibility...)
	return snap
}
