package slots

import (
	"sync"
	"time"
)

// weekDuration 是一周的时长。
const weekDuration = 7 * 24 * time.Hour

// Coordinator 是机场起降时刻协调器。所有变更操作携带当前时刻，
// 通过同一把互斥锁串行化，因此并发调用的结果等价于某个串行顺序；
// 被拒绝的操作不推进时钟、不改变任何分配、名单与登记。
type Coordinator struct {
	mu  sync.Mutex
	cfg Config

	last    time.Time
	started bool

	requests []*Request
	series   []*Series

	eligibles []Eligibility
	usedElig  []bool

	rem     []int
	reserve []int

	waitlist map[int][]int

	allocated bool
	settled   bool

	cellChecks   int64
	weeksScanned int64
}

// NewCoordinator 创建协调器。historic 为上一航季结算出的历史资格清单。
func NewCoordinator(cfg Config, historic []Eligibility) (*Coordinator, error) {
	if cfg.TotalWeeks < 1 || cfg.MinWeeks < 1 || cfg.MinWeeks > cfg.TotalWeeks ||
		cfg.Capacity < 1 || cfg.UsageThresholdPercent < 0 || cfg.UsageThresholdPercent > 100 ||
		cfg.NewEntrantThreshold < 0 || cfg.RegisterWindow < 0 {
		return nil, ErrInvalidParam
	}
	cells := cfg.TotalWeeks * 7 * 24
	c := &Coordinator{
		cfg:       cfg,
		eligibles: append([]Eligibility(nil), historic...),
		usedElig:  make([]bool, len(historic)),
		rem:       make([]int, cells),
		reserve:   make([]int, cells),
		waitlist:  map[int][]int{},
	}
	for i := range c.rem {
		c.rem[i] = cfg.Capacity
	}
	return c, nil
}

// cellIndex 返回（周, 星期几, 小时段）单元格的下标，查询为 O(1)。
func (c *Coordinator) cellIndex(week, weekday, hour int) int {
	return ((week-1)*7+weekday)*24 + hour
}

// weekEnd 返回第 w 周的结束时刻。
func (c *Coordinator) weekEnd(w int) time.Time {
	return c.cfg.SeasonStart.Add(time.Duration(w) * weekDuration)
}

// seasonEnd 返回航季结束时刻。
func (c *Coordinator) seasonEnd() time.Time { return c.weekEnd(c.cfg.TotalWeeks) }

// checkClock 校验时钟不回退。
func (c *Coordinator) checkClock(now time.Time) error {
	if c.started && now.Before(c.last) {
		return ErrClockRegression
	}
	return nil
}

// advance 在被接受的操作后推进时钟。
func (c *Coordinator) advance(now time.Time) {
	c.last = now
	c.started = true
}

// eachCell 对覆盖 [startWeek, endWeek] 每周同一（星期几, 小时段）的单元格调用 fn。
func eachCell(weekday, hour, startWeek, endWeek int, fn func(week, idx int)) {
	for w := startWeek; w <= endWeek; w++ {
		fn(w, ((w-1)*7+weekday)*24+hour)
	}
}

// fitsPlain 判定覆盖的每个单元格是否都有剩余容量（恰等于上限时不可再分配）。
func (c *Coordinator) fitsPlain(weekday, hour, startWeek, endWeek int) bool {
	ok := true
	eachCell(weekday, hour, startWeek, endWeek, func(_, idx int) {
		c.cellChecks++
		if c.rem[idx] <= 0 {
			ok = false
		}
	})
	return ok
}

// occupy 在覆盖的每个单元格上扣减一个剩余容量。
func (c *Coordinator) occupy(weekday, hour, startWeek, endWeek int) {
	eachCell(weekday, hour, startWeek, endWeek, func(_, idx int) { c.rem[idx]-- })
}

// newSeries 把申请转为系列并占用容量。
func (c *Coordinator) newSeries(holder string, weekday, hour, startWeek, endWeek int) *Series {
	weeks := endWeek - startWeek + 1
	s := &Series{
		ID:        len(c.series),
		Weekday:   weekday,
		Hour:      hour,
		StartWeek: startWeek,
		EndWeek:   endWeek,
		Holder:    holder,
		returned:  make([]uint8, weeks),
		status:    make([]WeekStatus, weeks),
	}
	c.series = append(c.series, s)
	c.occupy(weekday, hour, startWeek, endWeek)
	return s
}

// findSeries 按 ID 与持有者查找系列。
func (c *Coordinator) findSeries(id int, holder string) (*Series, error) {
	if id < 0 || id >= len(c.series) {
		return nil, ErrNotFound
	}
	s := c.series[id]
	if s.Holder != holder {
		return nil, ErrNotFound
	}
	return s, nil
}

// Remaining 返回某周某天某小时段的剩余容量，开销为 O(1)。
func (c *Coordinator) Remaining(week, weekday, hour int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rem[c.cellIndex(week, weekday, hour)]
}

// Waitlist 返回某小时段等候名单中的申请 ID（按提交时刻排序）。
func (c *Coordinator) Waitlist(weekday, hour int) []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.waitlist[weekday*24+hour]...)
}

// SeriesSnapshot 返回系列快照，供外部核对。
type SeriesSnapshot struct {
	ID        int
	Holder    string
	Weekday   int
	Hour      int
	StartWeek int
	EndWeek   int
}

// Snapshot 返回全部系列的快照（按系列 ID 次序）。
func (c *Coordinator) Snapshot() []SeriesSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]SeriesSnapshot, 0, len(c.series))
	for _, s := range c.series {
		out = append(out, SeriesSnapshot{s.ID, s.Holder, s.Weekday, s.Hour, s.StartWeek, s.EndWeek})
	}
	return out
}
