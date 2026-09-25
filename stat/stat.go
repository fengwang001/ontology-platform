// Package stat 记录每租户执行份额并度量加权公平性。
package stat

import "time"

// Clock 是可注入的时间源；nil 时使用墙钟。
type Clock interface{ Now() time.Time }

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// Row 是单个租户的累计统计。
type Row struct {
	ID      string
	Weight  float64
	Runs    int64
	Cost    float64
	LastRun time.Time
}

// Snapshot 是某时刻的全部租户统计。
type Snapshot struct {
	Rows []Row
}

// Tracker 按租户累计执行次数与实际代价（由上层加锁）。
type Tracker struct {
	clock Clock
	rows  map[string]*Row
	order []string
	cmps  int
}

// NewTracker 创建统计器。
func NewTracker(clock Clock) *Tracker {
	if clock == nil {
		clock = wallClock{}
	}
	return &Tracker{clock: clock, rows: map[string]*Row{}}
}

// Add 登记新租户。
func (t *Tracker) Add(id string, weight float64) {
	if _, ok := t.rows[id]; ok {
		return
	}
	t.rows[id] = &Row{ID: id, Weight: weight}
	t.order = append(t.order, id)
}

// Record 记录一次执行。
func (t *Tracker) Record(id string, cost float64, comparisons int) {
	r := t.rows[id]
	r.Runs++
	r.Cost += cost
	r.LastRun = t.clock.Now()
	t.cmps = comparisons
}

// LastComparisons 返回最近一次选择的比较次数。
func (t *Tracker) LastComparisons() int { return t.cmps }

// Snapshot 返回按注册顺序排列的统计副本。
func (t *Tracker) Snapshot() Snapshot {
	rows := make([]Row, 0, len(t.order))
	for _, id := range t.order {
		rows = append(rows, *t.rows[id])
	}
	return Snapshot{Rows: rows}
}

// ShareDeviations 返回每租户实际代价占比相对权重占比的相对偏差。
// 返回 map[租户]偏差（>=0）与总实际代价。
func (s Snapshot) ShareDeviations() (map[string]float64, float64) {
	var totalCost, totalWeight float64
	for _, r := range s.Rows {
		totalCost += r.Cost
		totalWeight += r.Weight
	}
	out := make(map[string]float64, len(s.Rows))
	for _, r := range s.Rows {
		want := r.Weight / totalWeight
		got := 0.0
		if totalCost > 0 {
			got = r.Cost / totalCost
		}
		dev := 0.0
		if want > 0 {
			d := got - want
			if d < 0 {
				d = -d
			}
			dev = d / want
		}
		out[r.ID] = dev
	}
	return out, totalCost
}
