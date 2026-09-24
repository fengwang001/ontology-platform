// Package stat 统计各租户实际执行代价占比，并度量与权重占比的偏差。
package stat

// Meter 累积每租户已执行代价，对比权重份额。非并发安全，由调用方串行化。
type Meter struct {
	weight map[string]float64
	cost   map[string]float64
	total  float64
	wsum   float64
}

// New 创建空统计器。
func New() *Meter {
	return &Meter{weight: make(map[string]float64), cost: make(map[string]float64)}
}

// SetWeight 登记租户权重（重复调用覆盖并修正权重和）。
func (m *Meter) SetWeight(id string, w float64) {
	m.wsum += w - m.weight[id]
	m.weight[id] = w
}

// Record 记录租户一次任务的真实执行代价。
func (m *Meter) Record(id string, cost float64) {
	m.cost[id] += cost
	m.total += cost
}

// Share 返回租户实际执行代价占比。
func (m *Meter) Share(id string) float64 {
	if m.total == 0 {
		return 0
	}
	return m.cost[id] / m.total
}

// Want 返回租户权重占比（理论份额）。
func (m *Meter) Want(id string) float64 {
	if m.wsum == 0 {
		return 0
	}
	return m.weight[id] / m.wsum
}

// RelDeviation 返回 |实际占比-理论占比|/理论占比。
func (m *Meter) RelDeviation(id string) float64 {
	want := m.Want(id)
	if want == 0 {
		return 0
	}
	d := m.Share(id)/want - 1
	if d < 0 {
		return -d
	}
	return d
}

// MaxRelDeviation 返回所有登记租户中相对偏差的最大值与对应租户。
func (m *Meter) MaxRelDeviation() (string, float64) {
	worst, max := "", 0.0
	for id := range m.weight {
		if d := m.RelDeviation(id); d > max {
			worst, max = id, d
		}
	}
	return worst, max
}
