package traffic

import "sort"

// naiveIncident 是朴素模型中的事件记录。
type naiveIncident struct {
	id        int
	link      int
	start     float64
	reduction float64
	ended     bool
}

// NaiveModel 是独立编写的朴素逐时刻推进参照模型。
// 它不使用 Service 的分段线性/定点求解，而是以固定小步长 dt 逐格推进，
// 每步用朴素的反复扫描松弛重算有效通行能力与等级，用于随机对照。
type NaiveModel struct {
	net      *Network
	n        int
	links    []*Link
	index    map[int]int
	capVeh   []float64
	q        []float64
	full     []bool
	inc      map[int]*naiveIncident
	active   map[int]*naiveIncident
	upstream [][]int
	now      float64
	dt       float64
}

// NewNaive 基于同一静态路网构建朴素模型，dt 为时间步长。
func NewNaive(net *Network, dt float64) *NaiveModel {
	ids := make([]int, 0, len(net.links))
	for id := range net.links {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	index := make(map[int]int, len(ids))
	links := make([]*Link, len(ids))
	capVeh := make([]float64, len(ids))
	up := make([][]int, len(ids))
	for i, id := range ids {
		index[id] = i
		links[i] = net.links[id]
		capVeh[i] = net.capacityInVehicles(links[i])
	}
	for i, id := range ids {
		ids2 := net.upstreamOf(id)
		up[i] = make([]int, len(ids2))
		for j, u := range ids2 {
			up[i][j] = index[u]
		}
	}
	return &NaiveModel{
		net: net, n: len(ids), links: links, index: index,
		capVeh: capVeh, q: make([]float64, len(ids)),
		full:     make([]bool, len(ids)),
		inc:      make(map[int]*naiveIncident),
		active:   make(map[int]*naiveIncident),
		upstream: up, dt: dt,
	}
}

// Register 登记事件。
func (m *NaiveModel) Register(id, link int, start, reduction float64) {
	m.advanceTo(start)
	m.inc[id] = &naiveIncident{id: id, link: link, start: start, reduction: reduction}
	m.refreshActive()
}

// UpdateReduction 在 at 时刻修改削减比例。
func (m *NaiveModel) UpdateReduction(id int, at, reduction float64) {
	m.advanceTo(at)
	m.inc[id].reduction = reduction
}

// Resolve 在 at 时刻解除事件。
func (m *NaiveModel) Resolve(id int, at float64) {
	m.advanceTo(at)
	m.inc[id].ended = true
	m.refreshActive()
}

// advanceTo 逐格推进；at 必须落在 dt 网格上。
func (m *NaiveModel) advanceTo(at float64) {
	for m.now < at-eps/2 {
		m.step()
	}
	m.now = at
}

// Now 返回当前时刻。
func (m *NaiveModel) Now() float64 { return m.now }

func (m *NaiveModel) refreshActive() {
	m.active = make(map[int]*naiveIncident, len(m.inc))
	for _, in := range m.inc {
		if !in.ended && in.start <= m.now+eps {
			m.active[in.id] = in
		}
	}
}

func (m *NaiveModel) step() {
	// 先激活本步起点已到的事件。
	for _, in := range m.inc {
		if !in.ended && !containsInc(m.active, in.id) && in.start <= m.now+eps {
			m.active[in.id] = in
		}
	}
	eff, _ := m.solve()
	next := make([]float64, m.n)
	for i := 0; i < m.n; i++ {
		rate := m.links[i].Arrival - eff[i]
		v := m.q[i] + rate*m.dt
		if m.full[i] {
			// 已回溢路段保持容量，直到净消散使其真正离开容量。
			if rate >= -eps || v >= m.capVeh[i]-m.snapTol(rate, i) {
				v = m.capVeh[i]
			}
			m.full[i] = v >= m.capVeh[i]-m.snapTol(rate, i)
		} else {
			if v <= m.snapTol(rate, i) {
				v = 0
			}
			if v >= m.capVeh[i]-m.snapTol(rate, i) {
				v = m.capVeh[i]
				m.full[i] = true
			}
		}
		next[i] = v
	}
	m.q = next
	m.now += m.dt
}

// snapTol 给出把一个格点吸附到 0/容量边界的容差（半个步长可走的距离）。
func (m *NaiveModel) snapTol(rate float64, i int) float64 {
	d := absF(rate) * m.dt / 2
	if d < 1e-9 {
		return 1e-9
	}
	return d
}

// solve 用朴素反复扫描松弛求有效通行能力，用朴素 BFS 求等级。
func (m *NaiveModel) solve() ([]float64, []int) {
	eff := make([]float64, m.n)
	for i, l := range m.links {
		eff[i] = l.Capacity
	}
	red := make(map[int]float64)
	for _, in := range m.active {
		i := m.index[in.link]
		if c := m.links[i].Capacity * (1 - in.reduction); red[i] == 0 || c < red[i] {
			red[i] = c
		}
	}
	for i, c := range red {
		eff[i] = c
	}
	for iter := 0; iter < m.n+2; iter++ {
		changed := false
		for i := 0; i < m.n; i++ {
			if !m.full[i] {
				continue
			}
			for _, j := range m.upstream[i] {
				if eff[i] < eff[j]-eps {
					eff[j] = eff[i]
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	level := make([]int, m.n)
	var q []int
	for _, in := range m.active {
		i := m.index[in.link]
		if level[i] == 0 || 1 < level[i] {
			level[i] = 1
		}
	}
	for i := 0; i < m.n; i++ {
		if level[i] == 1 {
			q = append(q, i)
		}
	}
	for head := 0; head < len(q); head++ {
		i := q[head]
		if !m.full[i] {
			continue
		}
		for _, j := range m.upstream[i] {
			if level[j] == 0 || level[i]+1 < level[j] {
				level[j] = level[i] + 1
				q = append(q, j)
			}
		}
	}
	return eff, level
}

// Snapshot 返回当前时刻每条路段的排队长度、有效通行能力与等级。
func (m *NaiveModel) Snapshot() ([]LinkState, []float64, []int) {
	eff, level := m.solve()
	out := make([]LinkState, m.n)
	for i := range out {
		out[i] = LinkState{Link: m.links[i].ID, Queue: m.q[i],
			EffectiveCap: eff[i], Level: level[i]}
	}
	return out, eff, level
}

func containsInc(set map[int]*naiveIncident, id int) bool { _, ok := set[id]; return ok }

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
