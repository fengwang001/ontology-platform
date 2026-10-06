package dr

import (
	"math/big"
	"sort"
	"sync"
)

// settledRange 记录已考核事件对某参与者的数据相关范围：
// 事件日及之前、日内间隔落在 [adjTod, endTod) 的数据不得再登记。
type settledRange struct {
	day            int
	adjTod, endTod int
}

// System 为需求响应系统门面。所有操作在内部互斥锁下串行化，
// 并发调用等价于某个串行执行顺序。
type System struct {
	mu  sync.Mutex
	cfg Config
	ipd int // 每天间隔数

	lastTick Tick
	clockSet bool

	events map[string]*Event

	byEvent map[string]map[string]*Commitment // eventID -> participant -> commitment
	active  map[string]map[string]*Commitment // participant -> eventID -> 生效中的承诺

	usage    map[string]map[int]int64 // participant -> 绝对间隔索引 -> 电量
	dataDays map[string][]int         // participant -> 有数据的日（升序去重）
	excluded map[string]map[int]int   // participant -> 日 -> 资格日排除引用计数

	settledRanges map[string][]settledRange // participant -> 已考核相关范围
	settlements   map[string]*Settlement

	adjMin, adjMax *big.Rat

	Stats Stats
}

// NewSystem 校验配置并创建系统。
func NewSystem(cfg Config) (*System, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &System{
		cfg:           cfg,
		ipd:           1440 / cfg.IntervalMinutes,
		events:        map[string]*Event{},
		byEvent:       map[string]map[string]*Commitment{},
		active:        map[string]map[string]*Commitment{},
		usage:         map[string]map[int]int64{},
		dataDays:      map[string][]int{},
		excluded:      map[string]map[int]int{},
		settledRanges: map[string][]settledRange{},
		settlements:   map[string]*Settlement{},
		adjMin:        cfg.AdjRatioMin.rat(),
		adjMax:        cfg.AdjRatioMax.rat(),
	}, nil
}

// checkClock 校验时钟单调性；通过后推进时钟。被拒绝时不改变时钟。
func (s *System) checkClock(now Tick) *OpError {
	if now < 0 {
		return opErr(ErrParam, "时刻不能为负: %d", now)
	}
	if s.clockSet && now < s.lastTick {
		return opErr(ErrClock, "时刻 %d 早于上一已接受操作时刻 %d", now, s.lastTick)
	}
	s.lastTick = now
	s.clockSet = true
	return nil
}

func (s *System) dayOf(interval int) int { return interval / s.ipd }
func (s *System) todOf(interval int) int { return interval % s.ipd }

// intervalOf 返回时刻所在间隔的绝对索引（向下取整）。
func (s *System) intervalOf(t Tick) int { return int(t) / s.cfg.IntervalMinutes }

func (s *System) tickOf(interval int) Tick { return Tick(interval * s.cfg.IntervalMinutes) }

// windowTods 返回事件调整期起点与窗口终点的日内间隔索引。
func (s *System) windowTods(e *Event) (adjTod, startTod, endTod int) {
	startTod = s.todOf(e.Start)
	endTod = s.todOf(e.End)
	adjTod = startTod - s.cfg.AdjustmentIntervals
	return adjTod, startTod, endTod
}

func (s *System) getEvent(id string) (*Event, *OpError) {
	e, ok := s.events[id]
	if !ok {
		return nil, opErr(ErrState, "事件不存在: %s", id)
	}
	return e, nil
}

// GetEvent 返回事件快照。
func (s *System) GetEvent(id string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.events[id]
	if !ok {
		return Event{}, false
	}
	return *e, true
}

// GetCommitment 返回承诺快照。
func (s *System) GetCommitment(eventID, participant string) (Commitment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.byEvent[eventID]
	if !ok {
		return Commitment{}, false
	}
	c, ok := m[participant]
	if !ok {
		return Commitment{}, false
	}
	return *c, true
}

// GetSettlement 返回已考核结果（深拷贝，不可变）。
func (s *System) GetSettlement(eventID string) (*Settlement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.settlements[eventID]
	if !ok {
		return nil, false
	}
	return cloneSettlement(st), true
}

func cloneSettlement(st *Settlement) *Settlement {
	out := &Settlement{EventID: st.EventID, Results: make([]ParticipantResult, len(st.Results))}
	for i, r := range st.Results {
		out.Results[i] = ParticipantResult{
			Participant: r.Participant,
			Assessable:  r.Assessable,
			Curtailment: cloneRat(r.Curtailment),
			Performance: cloneRat(r.Performance),
			Payment:     cloneRat(r.Payment),
			Penalty:     cloneRat(r.Penalty),
		}
	}
	return out
}

func cloneRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}

// insertDay 将日插入升序去重切片。
func insertDay(days []int, d int) []int {
	i := sort.SearchInts(days, d)
	if i < len(days) && days[i] == d {
		return days
	}
	days = append(days, 0)
	copy(days[i+1:], days[i:])
	days[i] = d
	return days
}

// ResetStats 清零性能计数器。
func (s *System) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Stats = Stats{}
}

// SnapshotStats 返回性能计数器快照。
func (s *System) SnapshotStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Stats
}

