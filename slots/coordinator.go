package slots

import (
	"io"
	"sort"
	"sync"
	"time"
)

// Coordinator 是单机场单航季的时刻协调器，所有变更方法并发安全，
// 结果等价于某个满足时间戳非降约束的串行顺序。
type Coordinator struct {
	mu sync.Mutex

	cfg      Config
	phase    phase
	lastTime time.Time
	clockSet bool

	airlines     map[string]bool
	applications map[string]*Application
	histAvail    map[string]map[HistKey]bool
	appSeq       int

	series map[string]*Series
	occ    map[cellKey]int
	wait   map[slotKey][]*waitEntry
	held   map[string]int

	seriesSeq int
	quals     []Qualification

	log io.Writer
}

// NewCoordinator 依据配置与上一航季历史资格创建协调器。
func NewCoordinator(cfg Config, hist map[string][]HistKey, log io.Writer) (*Coordinator, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	ha := map[string]map[HistKey]bool{}
	for airline, keys := range hist {
		set := map[HistKey]bool{}
		for _, k := range keys {
			if !cfg.validRange(k.Day, k.Hour, k.StartWeek, k.EndWeek) {
				return nil, ErrInvalid
			}
			set[k] = true
		}
		ha[airline] = set
	}
	return &Coordinator{
		cfg:          cfg,
		phase:        phaseAccepting,
		airlines:     map[string]bool{},
		applications: map[string]*Application{},
		histAvail:    ha,
		series:       map[string]*Series{},
		occ:          map[cellKey]int{},
		wait:         map[slotKey][]*waitEntry{},
		held:         map[string]int{},
		log:          log,
	}, nil
}

// RegisterAirline 登记一家航空公司。
func (c *Coordinator) RegisterAirline(at time.Time, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tracef(c.log, "RegisterAirline in {at:%s id:%q}", at.Format(time.RFC3339), id)
	if id == "" {
		tracef(c.log, "RegisterAirline out -> %s", ErrInvalid)
		return ErrInvalid
	}
	if err := c.advance(at); err != nil {
		tracef(c.log, "RegisterAirline out -> %s", err)
		return err
	}
	if c.airlines[id] {
		tracef(c.log, "RegisterAirline out -> %s", ErrInvalid)
		return ErrInvalid
	}
	c.airlines[id] = true
	c.commit(at)
	tracef(c.log, "RegisterAirline out -> accepted")
	return nil
}

// Apply 在申请截止前提交一条时刻系列申请，返回申请 ID。
func (c *Coordinator) Apply(at time.Time, airline string, day, hour, startWeek, endWeek int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tracef(c.log, "Apply in {at:%s airline:%q day:%d hour:%d weeks:%d-%d}",
		at.Format(time.RFC3339), airline, day, hour, startWeek, endWeek)
	if !c.cfg.validRange(day, hour, startWeek, endWeek) || airline == "" {
		tracef(c.log, "Apply out -> %s", ErrInvalid)
		return "", ErrInvalid
	}
	if err := c.advance(at); err != nil {
		tracef(c.log, "Apply out -> %s", err)
		return "", err
	}
	if !c.airlines[airline] {
		tracef(c.log, "Apply out -> %s", ErrNotFound)
		return "", ErrNotFound
	}
	if c.phase != phaseAccepting || at.After(c.cfg.ApplyDeadline) {
		tracef(c.log, "Apply out -> %s", ErrApplyClosed)
		return "", ErrApplyClosed
	}
	c.appSeq++
	id := appID(c.appSeq)
	c.applications[id] = &Application{
		ID: id, Airline: airline, Day: day, Hour: hour,
		StartWeek: startWeek, EndWeek: endWeek, SubmittedAt: at,
	}
	c.commit(at)
	tracef(c.log, "Apply out -> id:%s accepted", id)
	return id, nil
}

// SettleAllocation 在申请截止后执行一次性容量分配。
func (c *Coordinator) SettleAllocation(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tracef(c.log, "SettleAllocation in {at:%s}", at.Format(time.RFC3339))
	if err := c.advance(at); err != nil {
		tracef(c.log, "SettleAllocation out -> %s", err)
		return err
	}
	if c.phase == phaseSettled {
		tracef(c.log, "SettleAllocation out -> %s", ErrSeasonSettled)
		return ErrSeasonSettled
	}
	if c.phase == phaseAllocated || at.Before(c.cfg.ApplyDeadline) {
		tracef(c.log, "SettleAllocation out -> %s", ErrApplyClosed)
		return ErrApplyClosed
	}

	apps := sortedApps(c.applications)
	satisfied := map[string]bool{}
	tent := map[cellKey]int{}

	// 第一阶段：全部历史优先权申请。历史申请之间超容量即参数非法，整体不生效。
	var histApps []*Application
	for _, a := range apps {
		if set := c.histAvail[a.Airline]; set != nil && set[histKeyFor(a)] {
			histApps = append(histApps, a)
		}
	}
	for _, a := range histApps {
		if !fits(c.cfg, tent, a) {
			tracef(c.log, "SettleAllocation out -> %s (hist over capacity: %s)", ErrInvalid, a.ID)
			return ErrInvalid
		}
		for w := a.StartWeek; w <= a.EndWeek; w++ {
			tent[cellKey{w, a.Day, a.Hour}]++
		}
	}
	// 全部历史申请互不超容量后才落盘，保证该操作被拒绝时不产生任何分配。
	for _, a := range histApps {
		satisfied[a.ID] = true
		c.assign(a)
	}

	// 历史分配后的占用即基准占用，保留额随之固定。
	base := map[cellKey]int{}
	for k, v := range c.occ {
		base[k] = v
	}
	// 每满足一个新进入者，覆盖单元格的保留额各减一且不低于零，
	// 因此保留额在此处是逐格可变的。
	curResv := map[cellKey]int{}
	resvOf := func(k cellKey) int {
		if v, ok := curResv[k]; ok {
			return v
		}
		v := (c.cfg.Capacity[k.day][k.hour] - base[k]) / 2
		curResv[k] = v
		return v
	}

	canEntrant := func(a *Application) bool {
		for w := a.StartWeek; w <= a.EndWeek; w++ {
			k := cellKey{w, a.Day, a.Hour}
			if c.occ[k] >= c.cfg.Capacity[k.day][k.hour] || resvOf(k) <= 0 {
				return false
			}
		}
		return true
	}
	canGeneral := func(a *Application) bool {
		for w := a.StartWeek; w <= a.EndWeek; w++ {
			k := cellKey{w, a.Day, a.Hour}
			if c.cfg.Capacity[k.day][k.hour]-c.occ[k]-resvOf(k) <= 0 {
				return false
			}
		}
		return true
	}

	// 第二阶段：新进入者申请，按提交时刻（含 ID）先满足，逐格扣减保留额。
	for _, a := range apps {
		if satisfied[a.ID] || c.heldCount(a.Airline) >= c.cfg.NewEntrantThreshold {
			continue
		}
		if canEntrant(a) {
			satisfied[a.ID] = true
			c.assign(a)
			for w := a.StartWeek; w <= a.EndWeek; w++ {
				k := cellKey{w, a.Day, a.Hour}
				if curResv[k] > 0 {
					curResv[k]--
				}
			}
		}
	}

	// 第三阶段：其余申请，按提交时刻处理，只能使用非保留容量。
	for _, a := range apps {
		if satisfied[a.ID] {
			continue
		}
		if canGeneral(a) {
			satisfied[a.ID] = true
			c.assign(a)
		}
	}

	// 未满足者进入该小时段等候名单，按提交时刻排序。
	for _, a := range apps {
		if !satisfied[a.ID] {
			k := slotKey{a.Day, a.Hour}
			c.wait[k] = append(c.wait[k], &waitEntry{app: a})
		}
	}

	c.phase = phaseAllocated
	c.commit(at)
	tracef(c.log, "SettleAllocation out -> satisfied:%d waitlisted:%d",
		len(satisfied), len(apps)-len(satisfied))
	return nil
}

func (c *Coordinator) advance(at time.Time) error {
	if c.clockSet && at.Before(c.lastTime) {
		return ErrClock
	}
	return nil
}

func (c *Coordinator) commit(at time.Time) {
	c.lastTime = at
	c.clockSet = true
}

func (c *Coordinator) heldCount(airline string) int {
	return c.held[airline]
}

// assign 把已满足的申请落为系列并占用其全部单元格，系列 ID 按提交次序确定。
func (c *Coordinator) assign(a *Application) *Series {
	s := &Series{
		ID:         c.newSeriesID(),
		Airline:    a.Airline,
		Day:        a.Day,
		Hour:       a.Hour,
		StartWeek:  a.StartWeek,
		EndWeek:    a.EndWeek,
		returned:   map[int]bool{},
		returnedAt: map[int]time.Time{},
		register:   map[int]RegStatus{},
	}
	c.series[s.ID] = s
	c.held[s.Airline]++
	for w := a.StartWeek; w <= a.EndWeek; w++ {
		c.occ[cellKey{w, a.Day, a.Hour}]++
	}
	tracef(c.log, "  assign %s -> %s airline:%q", a.ID, s.ID, a.Airline)
	return s
}

func sortedApps(m map[string]*Application) []*Application {
	out := make([]*Application, 0, len(m))
	for _, a := range m {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].SubmittedAt.Equal(out[j].SubmittedAt) {
			return out[i].SubmittedAt.Before(out[j].SubmittedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// fits 判定一个申请在给定临时占用之上是否所有单元格仍有剩余。
func fits(cfg Config, occ map[cellKey]int, a *Application) bool {
	for w := a.StartWeek; w <= a.EndWeek; w++ {
		k := cellKey{w, a.Day, a.Hour}
		if occ[k] >= cfg.Capacity[k.day][k.hour] {
			return false
		}
	}
	return true
}

func (c *Coordinator) newSeriesID() string {
	c.seriesSeq++
	return seriesID(c.seriesSeq)
}

func seriesID(n int) string { return "S" + itoa(n) }

func appID(n int) string { return "A" + itoa(n) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func sortedSeries(ss map[string]*Series) []*Series {
	out := make([]*Series, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var _ = sort.IntSlice{}
