package station

import "sort"

// naiveSess 是朴素模型的会话结构。
type naiveSess struct {
	id            string
	port          string
	plug          int64
	pri           Priority
	state         State
	energy        int
	demand        int
	minPwr        int
	cap           int
	pwr           int
	demotedThisEv bool
}

// naiveModel 严格按规格文字逐秒重放：
// 分配用「逐单位轮询发放」实现水位份额，推进用逐秒循环。
type naiveModel struct {
	now      int
	cap      int
	ports    map[string]int
	portSess map[string]string
	sess     map[string]*naiveSess
	plugSeq  int64
	pending  []capChange
}

func newNaive(cap0 int, ports []Port) *naiveModel {
	m := &naiveModel{
		cap:      cap0,
		ports:    map[string]int{},
		portSess: map[string]string{},
		sess:     map[string]*naiveSess{},
	}
	for _, p := range ports {
		if p.MaxPwr >= 0 {
			m.ports[p.ID] = p.MaxPwr
		}
	}
	return m
}

func (m *naiveModel) plug(p PlugParams) (string, bool) {
	if p.Demand <= 0 || p.CarMax <= 0 || p.MinPwr < 0 || p.MinPwr > p.CarMax ||
		(p.Priority != PriorityNormal && p.Priority != PriorityFast) {
		return "", false
	}
	if _, ok := m.ports[p.PortID]; !ok {
		return "", false
	}
	if _, busy := m.portSess[p.PortID]; busy {
		return "", false
	}
	id := p.SessionID
	if id != "" {
		if _, exists := m.sess[id]; exists {
			return "", false
		}
	}
	m.plugSeq++
	se := &naiveSess{
		id:     id,
		port:   p.PortID,
		plug:   m.plugSeq,
		pri:    p.Priority,
		state:  StateWaiting,
		demand: p.Demand,
		minPwr: p.MinPwr,
		cap:    minInt(p.CarMax, m.ports[p.PortID]),
	}
	m.sess[id] = se
	m.portSess[p.PortID] = id
	m.realloc(false, nil)
	return id, true
}

func (m *naiveModel) unplug(id string) (int, bool) {
	se, ok := m.sess[id]
	if !ok {
		return 0, false
	}
	e := se.energy
	delete(m.sess, id)
	delete(m.portSess, se.port)
	m.realloc(false, nil)
	return e, true
}

func (m *naiveModel) setPriority(id string, p Priority) bool {
	if p != PriorityNormal && p != PriorityFast {
		return false
	}
	se, ok := m.sess[id]
	if !ok {
		return false
	}
	if se.state == StateFull {
		return false
	}
	if se.pri == p {
		return true
	}
	demoted := map[string]bool{}
	if se.pri == PriorityFast && p == PriorityNormal {
		demoted[id] = true
		se.demotedThisEv = true
	}
	se.pri = p
	m.realloc(false, demoted)
	for _, x := range m.sess {
		x.demotedThisEv = false
	}
	return true
}

func (m *naiveModel) changeCap(at, newCap int) bool {
	if newCap < 0 || at < 0 || at < m.now {
		return false
	}
	seq := int64(len(m.pending)) + 1
	if at == m.now {
		m.cap = newCap
		m.realloc(true, nil)
		return true
	}
	m.pending = append(m.pending, capChange{at: at, cap: newCap, seq: seq})
	sort.SliceStable(m.pending, func(i, j int) bool {
		if m.pending[i].at != m.pending[j].at {
			return m.pending[i].at < m.pending[j].at
		}
		return m.pending[i].seq < m.pending[j].seq
	})
	return true
}

func (m *naiveModel) advance(t int) bool {
	if t < m.now {
		return false
	}
	for m.now < t {
		m.now++
		for _, se := range m.sortedSessions() {
			if se.state == StateCharging {
				add := se.pwr
				if add > se.demand-se.energy {
					add = se.demand - se.energy
				}
				se.energy += add
			}
		}
		// 充满：同时刻按插枪先后，每台充满后立即重新分配。
		for {
			var v *naiveSess
			for _, se := range m.sortedSessions() {
				if se.state == StateCharging && se.energy >= se.demand {
					v = se
					break
				}
			}
			if v == nil {
				break
			}
			v.state = StateFull
			v.pwr = 0
			m.realloc(false, nil)
		}
		// 上限变更在充满之后生效。
		for len(m.pending) > 0 && m.pending[0].at <= m.now {
			ch := m.pending[0]
			m.pending = m.pending[1:]
			m.cap = ch.cap
			m.realloc(true, nil)
		}
	}
	return true
}

func (m *naiveModel) sortedSessions() []*naiveSess {
	var list []*naiveSess
	for _, se := range m.sess {
		list = append(list, se)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].plug < list[j].plug })
	return list
}

// realloc 朴素分配：优先类别先于普通类别；类别内充电者先、等待者
// 按插枪序入池；逐轮水位分摊并按规则挤出，直至所有在池车辆达标。
func (m *naiveModel) realloc(capDown bool, demoted map[string]bool) {
	oldFast := 0
	for _, se := range m.sess {
		if se.pri == PriorityFast && se.state == StateCharging {
			oldFast += se.pwr
		}
	}
	fastPlan, fastUsed := m.allocClass(PriorityFast, m.cap, capDown, demoted)
	normalMay := capDown || fastUsed > oldFast
	normalPlan, _ := m.allocClass(PriorityNormal, m.cap-fastUsed, normalMay, demoted)
	for _, se := range m.sess {
		if se.state == StateFull {
			se.pwr = 0
			continue
		}
		var p int
		var ok bool
		if se.pri == PriorityFast {
			p, ok = fastPlan[se.id]
		} else {
			p, ok = normalPlan[se.id]
		}
		if ok {
			se.pwr = p
			se.state = StateCharging
		} else {
			se.pwr = 0
			se.state = StateWaiting
		}
	}
}

func (m *naiveModel) allocClass(pri Priority, budget int, maySuspend bool, demoted map[string]bool) (map[string]int, int) {
	var chargers, waiters []*naiveSess
	for _, se := range m.sortedSessions() {
		if se.state == StateFull || se.pri != pri {
			continue
		}
		isDemoted := demoted != nil && demoted[se.id]
		if se.state == StateCharging && !isDemoted {
			chargers = append(chargers, se)
		} else {
			waiters = append(waiters, se)
		}
	}
	pool := append(append([]*naiveSess{}, chargers...), waiters...)
	isCharger := map[string]bool{}
	for _, se := range chargers {
		isCharger[se.id] = true
	}
	removed := map[string]bool{}
	for {
		plan := m.naiveShare(pool, budget, removed)
		victim := m.pickVictim(pool, removed, isCharger, plan, maySuspend)
		if victim == "" {
			out := map[string]int{}
			used := 0
			for _, se := range pool {
				if !removed[se.id] {
					out[se.id] = plan[se.id]
					used += plan[se.id]
				}
			}
			return out, used
		}
		removed[victim] = true
	}
}

// naiveShare 逐单位把预算发给当前份额最小（同份额按插枪序）
// 且未到自身上限的车辆，等价于水位式整数公平分摊。
func (m *naiveModel) naiveShare(pool []*naiveSess, budget int, removed map[string]bool) map[string]int {
	plan := map[string]int{}
	var alive []*naiveSess
	for _, se := range pool {
		if !removed[se.id] {
			alive = append(alive, se)
		}
	}
	sort.Slice(alive, func(i, j int) bool { return alive[i].plug < alive[j].plug })
	if budget < 0 {
		budget = 0
	}
	for u := 0; u < budget; u++ {
		var best *naiveSess
		bestShare := 0
		for _, se := range alive {
			share := plan[se.id]
			if share >= se.cap {
				continue
			}
			if best == nil || share < bestShare {
				best = se
				bestShare = share
			}
		}
		if best == nil {
			break
		}
		plan[best.id]++
	}
	return plan
}

func (m *naiveModel) pickVictim(pool []*naiveSess, removed, isCharger map[string]bool, plan map[string]int, maySuspend bool) string {
	badWaiter, badCharger, latestWaiter := "", "", ""
	var bw, bc, lw int64 = -1, -1, -1
	for _, se := range pool {
		if removed[se.id] {
			continue
		}
		if !isCharger[se.id] && se.plug > lw {
			lw, latestWaiter = se.plug, se.id
		}
		if plan[se.id] >= se.minPwr {
			continue
		}
		if isCharger[se.id] {
			if se.plug > bc {
				bc, badCharger = se.plug, se.id
			}
		} else if se.plug > bw {
			bw, badWaiter = se.plug, se.id
		}
	}
	if badWaiter != "" {
		return badWaiter
	}
	if badCharger != "" {
		if maySuspend {
			return badCharger
		}
		if latestWaiter != "" {
			return latestWaiter
		}
		return badCharger
	}
	return ""
}

func (m *naiveModel) snapshot() Snapshot {
	snap := Snapshot{Now: m.now, Cap: m.cap}
	for id, mx := range m.ports {
		snap.Ports = append(snap.Ports, PortView{ID: id, MaxPwr: mx, SessionID: m.portSess[id]})
	}
	for _, se := range m.sess {
		snap.Sessions = append(snap.Sessions, Session{
			ID: se.id, PortID: se.port, PlugOrder: se.plug, Priority: se.pri,
			State: se.state, Energy: se.energy, Demand: se.demand,
			MinPwr: se.minPwr, CarMax: 0, Cap: se.cap, Pwr: se.pwr,
		})
	}
	sortViews(snap)
	return snap
}
