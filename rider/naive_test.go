package rider

import "sort"

// naiveModel 是独立实现的朴素对照模型：
// 每个被接受操作都保留原始记录，查询/结算/回溯时对该骑手全部存活事件
// 做全量重算（成簇用排序后相邻判定，结算逐周期重放）。
// 它刻意不与生产实现共享任何数据结构代码，只共享类型与错误码常量。

type nEvent struct {
	id      int64
	rider   string
	t       int64
	typ     EventType
	root    string
	seq     int64
	revoked bool
	appeal  int64
}

type nAppeal struct {
	id      int64
	rider   string
	eventID int64
	decided bool
	upheld  bool
}

type nPeriod struct {
	score    int
	level    int
	rights   int
	baseline int
	counting map[int64]int // 结算时刻的冻结贡献 eventID -> score
}

type nRider struct {
	periods   map[int]*nPeriod
	next      int
	lastRight int
	overrides map[int]int
}

type naiveModel struct {
	cfg     Config
	clock   int64
	riders  map[string]bool
	events  map[int64]*nEvent
	appeals map[int64]*nAppeal
	frozen  map[string]*nRider
	comps   []Compensation
	nextE   int64
	nextA   int64
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:     cfg,
		riders:  map[string]bool{},
		events:  map[int64]*nEvent{},
		appeals: map[int64]*nAppeal{},
		frozen:  map[string]*nRider{},
		nextE:   1,
		nextA:   1,
	}
}

type nResult struct {
	id   int64
	err  ErrorCode
	comp *Compensation
	q    QueryResult
}

func (m *naiveModel) levelOf(score int) int {
	lv := 0
	for _, th := range m.cfg.Thresholds {
		if score >= th {
			lv++
		} else {
			break
		}
	}
	return lv
}

func (m *naiveModel) periodOf(t int64) int {
	L := m.cfg.PeriodLength
	x := t - m.cfg.PeriodOrigin
	q := x / L
	if x%L != 0 && x < 0 {
		q--
	}
	return int(q)
}

func (m *naiveModel) rightOf(p int) int64 {
	return m.cfg.PeriodOrigin + int64(p+1)*m.cfg.PeriodLength
}

func (m *naiveModel) sortedEvents(rider string) []*nEvent {
	var evs []*nEvent
	for _, e := range m.events {
		if e.rider == rider && !e.revoked {
			evs = append(evs, e)
		}
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return evs[i].seq < evs[j].seq
	})
	return evs
}

// countingEvents 全量重算：返回当前计扣分事件 ID -> 分值，并返回同簇成员映射。
func (m *naiveModel) countingEvents(rider string) (map[int64]int, map[int64][]*nEvent) {
	evs := m.sortedEvents(rider)
	counting := map[int64]int{}
	membersOf := map[int64][]*nEvent{}
	byRoot := map[string][]*nEvent{}
	var standalone []*nEvent
	for _, e := range evs {
		if e.root == "" {
			standalone = append(standalone, e)
		} else {
			byRoot[e.root] = append(byRoot[e.root], e)
		}
	}
	for _, e := range standalone {
		counting[e.id] = m.cfg.EventScores[e.typ]
		membersOf[e.id] = []*nEvent{e}
	}
	for _, list := range byRoot {
		// list 已按 (t, seq) 有序；相邻差 <= span 即同簇，传递延伸。
		l := 0
		for i := 1; i <= len(list); i++ {
			if i == len(list) || list[i].t-list[i-1].t > m.cfg.ClusterSpan {
				cluster := list[l:i]
				first := cluster[0]
				counting[first.id] = m.cfg.EventScores[first.typ]
				for _, x := range cluster {
					membersOf[x.id] = cluster
				}
				l = i
			}
		}
	}
	return counting, membersOf
}

// settle 重放 now 之前应结算的周期；冻结快照与生产实现一致。
func (m *naiveModel) settle(rider string, now int64) *nRider {
	st := m.frozen[rider]
	if st == nil {
		st = &nRider{periods: map[int]*nPeriod{}, overrides: map[int]int{}}
		m.frozen[rider] = st
	}
	counting, _ := m.countingEvents(rider)
	for {
		p := st.next
		if m.rightOf(p) > now {
			break
		}
		score := 0
		frozen := map[int64]int{}
		for id, sc := range counting {
			if m.periodOf(m.events[id].t) == p {
				score += sc
				frozen[id] = sc
			}
		}
		level := m.levelOf(score)
		base := st.lastRight
		if ob, ok := st.overrides[p]; ok {
			base = ob
			delete(st.overrides, p)
		}
		capped := level
		if c := base + m.cfg.MaxLevelDrop; c < capped {
			capped = c
		}
		st.periods[p] = &nPeriod{score: score, level: level, rights: capped, baseline: base, counting: frozen}
		st.lastRight = capped
		st.next = p + 1
	}
	return st
}

func (m *naiveModel) registerRider(now int64, rider string) nResult {
	if rider == "" {
		return nResult{err: ErrInvalidArgument}
	}
	if now < m.clock {
		return nResult{err: ErrClockSkew}
	}
	if m.riders[rider] {
		return nResult{err: ErrInvalidArgument}
	}
	m.riders[rider] = true
	m.clock = now
	return nResult{}
}

func (m *naiveModel) registerEvent(now int64, rider string, t int64, typ EventType, root string) nResult {
	if rider == "" {
		return nResult{err: ErrInvalidArgument}
	}
	if _, ok := m.cfg.EventScores[typ]; !ok {
		return nResult{err: ErrInvalidArgument}
	}
	if now < m.clock {
		return nResult{err: ErrClockSkew}
	}
	if !m.riders[rider] {
		return nResult{err: ErrRiderNotFound}
	}
	m.settle(rider, now)
	id := m.nextE
	m.nextE++
	m.events[id] = &nEvent{id: id, rider: rider, t: t, typ: typ, root: root, seq: id}
	m.clock = now
	return nResult{id: id}
}

func (m *naiveModel) submitAppeal(now int64, rider string, eventID int64) nResult {
	if rider == "" {
		return nResult{err: ErrInvalidArgument}
	}
	if now < m.clock {
		return nResult{err: ErrClockSkew}
	}
	if !m.riders[rider] {
		return nResult{err: ErrRiderNotFound}
	}
	e, ok := m.events[eventID]
	if !ok || e.rider != rider {
		return nResult{err: ErrEventNotFound}
	}
	m.settle(rider, now)
	if e.revoked {
		return nResult{err: ErrEventRevoked}
	}
	if e.appeal != 0 {
		return nResult{err: ErrAlreadyAppealed}
	}
	if now < e.t || now >= e.t+m.cfg.AppealWindow {
		return nResult{err: ErrAppealWindowExpired}
	}
	counting, _ := m.countingEvents(rider)
	if e.root != "" {
		if _, ok := counting[eventID]; !ok {
			return nResult{err: ErrCompanionEvent}
		}
	}
	id := m.nextA
	m.nextA++
	m.appeals[id] = &nAppeal{id: id, rider: rider, eventID: eventID}
	e.appeal = id
	m.clock = now
	return nResult{id: id}
}

func (m *naiveModel) ruleAppeal(now int64, rider string, appealID int64, upheld bool) nResult {
	if rider == "" {
		return nResult{err: ErrInvalidArgument}
	}
	if now < m.clock {
		return nResult{err: ErrClockSkew}
	}
	if !m.riders[rider] {
		return nResult{err: ErrRiderNotFound}
	}
	a, ok := m.appeals[appealID]
	if !ok || a.rider != rider {
		return nResult{err: ErrAppealNotFound}
	}
	m.settle(rider, now)
	if a.decided {
		return nResult{err: ErrAppealDecided}
	}
	if m.events[a.eventID].revoked {
		return nResult{err: ErrEventRevoked}
	}
	a.decided = true
	a.upheld = upheld
	m.clock = now
	if !upheld {
		return nResult{}
	}
	e := m.events[a.eventID]
	_, membersOf := m.countingEvents(rider)
	members := membersOf[e.id]
	if members == nil {
		members = []*nEvent{e}
	}

	st := m.frozen[rider]
	affected := map[int]bool{}
	for _, x := range members {
		x.revoked = true
		p := m.periodOf(x.t)
		if ps := st.periods[p]; ps != nil {
			if _, frozen := ps.counting[x.id]; frozen {
				delete(ps.counting, x.id)
				affected[p] = true
			}
		}
	}

	var pList []int
	for p := range affected {
		pList = append(pList, p)
	}
	sort.Ints(pList)
	totalDiff := 0
	lastShadow := 0
	for _, p := range pList {
		ps := st.periods[p]
		would := 0
		for _, sc := range ps.counting {
			would += sc
		}
		wouldLevel := m.levelOf(would)
		shadow := wouldLevel
		if c := ps.baseline + m.cfg.MaxLevelDrop; c < shadow {
			shadow = c
		}
		if d := ps.rights - shadow; d > 0 {
			totalDiff += d
		}
		lastShadow = shadow
	}
	var comp *Compensation
	if totalDiff > 0 {
		comp = &Compensation{AppealID: appealID, RiderID: rider, Amount: totalDiff * m.cfg.CompensationPerLevel}
		m.comps = append(m.comps, *comp)
	}
	if len(pList) > 0 {
		start := m.periodOf(now)
		if m.rightOf(start) <= now {
			start++
		}
		prop := lastShadow
		for p := pList[len(pList)-1] + 1; p < start && p < st.next; p++ {
			shadow := st.periods[p].level
			if c := prop + m.cfg.MaxLevelDrop; c < shadow {
				shadow = c
			}
			prop = shadow
		}
		if cur, ok := st.overrides[start]; !ok || prop < cur {
			st.overrides[start] = prop
		}
	}
	return nResult{comp: comp}
}

func (m *naiveModel) query(now int64, rider string, p int) nResult {
	if now < m.clock {
		return nResult{err: ErrClockSkew}
	}
	if !m.riders[rider] {
		return nResult{err: ErrRiderNotFound}
	}
	st := m.settle(rider, now)
	out := QueryResult{Period: p}
	if ps := st.periods[p]; ps != nil {
		out.Settled = true
		out.Score = ps.score
		out.Level = ps.level
		out.RightsLevel = ps.rights
	} else {
		counting, _ := m.countingEvents(rider)
		for id, sc := range counting {
			if m.periodOf(m.events[id].t) == p {
				out.Score += sc
			}
		}
	}
	m.clock = now
	return nResult{q: out}
}
