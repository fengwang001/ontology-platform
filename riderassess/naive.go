package riderassess

// 朴素对照模型：与 System 暴露完全相同的操作，但每次调用都从完整的被接受
// 操作日志全量重建簇、扣分、冻结等级、权益与补偿账目。刻意写成"显而易见的
// 正确实现"：每个根因组内按时刻排序，扫描一遍切出传递簇，不做任何增量维护。
// 仅用于测试中的随机差分对比，不参与生产逻辑。

import "sort"

const (
	nopRider = iota
	nopEvent
	nopFile
	nopRule
	nopQuery
)

type naiveOp struct {
	kind    int
	ts      int64
	ev      Event
	id      string
	eventID string
	period  int
	upheld  bool
}

// NaiveModel 全量重算的朴素实现。
type NaiveModel struct {
	cfg Config
	log []naiveOp
}

// NewNaiveModel 构造朴素模型。
func NewNaiveModel(cfg Config) (*NaiveModel, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return &NaiveModel{cfg: cfg}, nil
}

type nEvent struct {
	ev       Event
	revoked  bool
	appealed bool
}

type nRider struct {
	totals       map[int]int
	scoreSnap    map[int]int
	gradeSnap    map[int]int
	benefitSnap  map[int]int
	baseOverride map[int]int
	nextFreeze   int
	comps        []Compensation
}

type nState struct {
	maxTs   int64
	riders  map[string]bool
	events  map[string]*nEvent
	appeals map[string]*Appeal
	rs      map[string]*nRider
}

func newNRider() *nRider {
	return &nRider{
		totals: map[int]int{}, scoreSnap: map[int]int{},
		gradeSnap: map[int]int{}, benefitSnap: map[int]int{},
		baseOverride: map[int]int{},
	}
}

// sortedKeys 返回某骑手某根因下全部未撤销事件的有序键。
func (st *nState) sortedKeys(rider, root string) (keys []eventKey) {
	for _, ne := range st.events {
		if !ne.revoked && ne.ev.Rider == rider && ne.ev.RootCause == root {
			keys = append(keys, eventKey{ne.ev.OccurAt, ne.ev.ID})
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keyLess(keys[i], keys[j]) })
	return keys
}

// rebuildScores 对一个骑手按当前全部未撤销事件全量重算簇与周期扣分。
func (m *NaiveModel) rebuildScores(st *nState, name string) {
	r := st.rs[name]
	r.totals = map[int]int{}
	add := func(k eventKey) {
		r.totals[m.cfg.periodIndex(k.at)] += m.cfg.Deductions[st.events[k.id].ev.Type]
	}
	// 无根因事件：各自独立计扣分。
	for _, ne := range st.events {
		if !ne.revoked && ne.ev.Rider == name && ne.ev.RootCause == "" {
			add(eventKey{ne.ev.OccurAt, ne.ev.ID})
		}
	}
	// 有根因事件：按根因分组排序，相邻差不超过跨度即同簇（传递延伸），每簇最早者计分。
	roots := map[string]bool{}
	for _, ne := range st.events {
		if !ne.revoked && ne.ev.Rider == name {
			roots[ne.ev.RootCause] = true
		}
	}
	for root := range roots {
		if root == "" {
			continue
		}
		keys := st.sortedKeys(name, root)
		add(keys[0])
		for i := 1; i < len(keys); i++ {
			if keys[i].at-keys[i-1].at > m.cfg.ClusterSpan {
				add(keys[i])
			}
		}
	}
}

// satellite 判断某事件是否为其根因簇的连带事件。
func (m *NaiveModel) satellite(st *nState, ne *nEvent) bool {
	if ne.ev.RootCause == "" {
		return false
	}
	keys := st.sortedKeys(ne.ev.Rider, ne.ev.RootCause)
	target := eventKey{ne.ev.OccurAt, ne.ev.ID}
	for i := 1; i < len(keys); i++ {
		if keys[i] == target {
			return keys[i].at-keys[i-1].at <= m.cfg.ClusterSpan
		}
	}
	return false
}

func settleBaseN(r *nRider, p int) int {
	if b, ok := r.baseOverride[p]; ok {
		return b
	}
	if p == 0 {
		return 0
	}
	return r.benefitSnap[p-1]
}

func (m *NaiveModel) liveBenefit(r *nRider, p int) int {
	if b, ok := r.benefitSnap[p]; ok {
		return b
	}
	first := p
	if r.nextFreeze < first {
		first = r.nextFreeze
	}
	base := settleBaseN(r, first)
	for k := first; k <= p; k++ {
		if v, ok := r.benefitSnap[k]; ok {
			base = v
			continue
		}
		if ov, ok := r.baseOverride[k]; ok {
			base = ov
		}
		base = clampDown(base, m.cfg.gradeOf(r.totals[k]), m.cfg.MaxDownPerCycle)
	}
	return base
}

func (m *NaiveModel) freezeThrough(r *nRider, now int64) {
	for m.cfg.periodEnd(r.nextFreeze) <= now {
		p := r.nextFreeze
		grade := m.cfg.gradeOf(r.totals[p])
		r.scoreSnap[p] = r.totals[p]
		r.gradeSnap[p] = grade
		r.benefitSnap[p] = clampDown(settleBaseN(r, p), grade, m.cfg.MaxDownPerCycle)
		r.nextFreeze = p + 1
	}
}

// simulate 重放截至（含）第 cutoff 条日志后的完整状态。
func (m *NaiveModel) simulate(cutoff int) *nState {
	st := &nState{
		riders: map[string]bool{}, events: map[string]*nEvent{},
		appeals: map[string]*Appeal{}, rs: map[string]*nRider{},
	}
	for i, op := range m.log {
		if i > cutoff {
			break
		}
		st.maxTs = op.ts
		switch op.kind {
		case nopRider:
			if !st.riders[op.id] {
				st.riders[op.id] = true
				st.rs[op.id] = newNRider()
			}
		case nopEvent:
			st.events[op.ev.ID] = &nEvent{ev: op.ev}
			if st.rs[op.ev.Rider] == nil {
				st.rs[op.ev.Rider] = newNRider()
			}
			m.rebuildScores(st, op.ev.Rider)
			m.freezeThrough(st.rs[op.ev.Rider], op.ts)
		case nopFile:
			ne := st.events[op.eventID]
			ne.appealed = true
			st.appeals[op.id] = &Appeal{ID: op.id, EventID: op.eventID, Rider: ne.ev.Rider, FiledAt: op.ts}
		case nopRule:
			a := st.appeals[op.id]
			a.Ruled = true
			a.Upheld = op.upheld
			a.RuledAt = op.ts
			if op.upheld {
				m.applyUpheld(st, op)
			}
		case nopQuery:
			if st.riders[op.id] {
				m.freezeThrough(st.rs[op.id], op.ts)
			}
		}
	}
	return st
}

// applyUpheld 执行申诉成立：整簇撤销、重算扣分、冻结、补偿与基准改写。
func (m *NaiveModel) applyUpheld(st *nState, op naiveOp) {
	target := st.events[op.eventID]
	var cluster []*nEvent
	if target.ev.RootCause == "" {
		cluster = []*nEvent{target}
	} else {
		keys := st.sortedKeys(target.ev.Rider, target.ev.RootCause)
		byKey := map[eventKey]*nEvent{}
		for _, k := range keys {
			byKey[k] = st.events[k.id]
		}
		tk := eventKey{target.ev.OccurAt, target.ev.ID}
		idx := sort.Search(len(keys), func(i int) bool { return !keyLess(keys[i], tk) })
		lo, hi := idx, idx
		for lo > 0 && keys[lo].at-keys[lo-1].at <= m.cfg.ClusterSpan {
			lo--
		}
		for hi+1 < len(keys) && keys[hi+1].at-keys[hi].at <= m.cfg.ClusterSpan {
			hi++
		}
		for k := lo; k <= hi; k++ {
			cluster = append(cluster, byKey[keys[k]])
		}
		sort.Slice(cluster, func(i, j int) bool {
			return keyLess(eventKey{cluster[i].ev.OccurAt, cluster[i].ev.ID},
				eventKey{cluster[j].ev.OccurAt, cluster[j].ev.ID})
		})
	}
	scorer := cluster[0]
	affectedPeriod := m.cfg.periodIndex(scorer.ev.OccurAt)
	for _, ne := range cluster {
		ne.revoked = true
	}
	m.rebuildScores(st, target.ev.Rider)
	r := st.rs[target.ev.Rider]
	m.freezeThrough(r, op.ts)
	if frozenGrade, frozen := r.gradeSnap[affectedPeriod]; frozen {
		desiredGrade := m.cfg.gradeOf(r.totals[affectedPeriod])
		if desiredGrade < frozenGrade {
			desiredBenefit := m.liveBenefit(r, affectedPeriod)
			r.comps = append(r.comps, Compensation{
				AppealID: op.id, Rider: target.ev.Rider, Period: affectedPeriod,
				FrozenGrade: frozenGrade, DesiredGrade: desiredGrade,
				Levels: frozenGrade - desiredGrade,
				Amount: (frozenGrade - desiredGrade) * m.cfg.CompPerLevel,
			})
			q := affectedPeriod + 1
			for m.cfg.periodEnd(q) <= op.ts {
				q++
			}
			if q >= r.nextFreeze {
				r.baseOverride[q] = desiredBenefit
			}
		}
	}
}

func (m *NaiveModel) checkClock(opTs int64) error {
	if len(m.log) > 0 && opTs < m.log[len(m.log)-1].ts {
		return errf(ErrClockRollback, "operation time %d earlier than accepted max", opTs)
	}
	return nil
}

// RegisterRider 注册骑手（幂等）。
func (m *NaiveModel) RegisterRider(opTs int64, rider string) error {
	if err := m.checkClock(opTs); err != nil {
		return err
	}
	if rider == "" {
		return errf(ErrInvalidParam, "rider id is empty")
	}
	m.log = append(m.log, naiveOp{kind: nopRider, ts: opTs, id: rider})
	return nil
}

// RegisterEvent 登记事件。
func (m *NaiveModel) RegisterEvent(opTs int64, ev Event) error {
	if err := m.checkClock(opTs); err != nil {
		return err
	}
	if ev.ID == "" || ev.Rider == "" {
		return errf(ErrInvalidParam, "event id/rider is empty")
	}
	st := m.simulate(len(m.log) - 1)
	if _, dup := st.events[ev.ID]; dup {
		return errf(ErrInvalidParam, "duplicate event id %q", ev.ID)
	}
	if ded, ok := m.cfg.Deductions[ev.Type]; !ok || ded <= 0 {
		return errf(ErrInvalidParam, "unknown or non-positive deduction type %q", ev.Type)
	}
	if !st.riders[ev.Rider] {
		return errf(ErrRiderNotFound, "rider %q not registered", ev.Rider)
	}
	m.log = append(m.log, naiveOp{kind: nopEvent, ts: opTs, ev: ev})
	return nil
}

// FileAppeal 提出申诉。
func (m *NaiveModel) FileAppeal(opTs int64, id, eventID string) error {
	if err := m.checkClock(opTs); err != nil {
		return err
	}
	if id == "" || eventID == "" {
		return errf(ErrInvalidParam, "appeal/event id is empty")
	}
	st := m.simulate(len(m.log) - 1)
	if _, dup := st.appeals[id]; dup {
		return errf(ErrInvalidParam, "duplicate appeal id %q", id)
	}
	ne, ok := st.events[eventID]
	if !ok {
		return errf(ErrEventNotFound, "event %q not found", eventID)
	}
	if !st.riders[ne.ev.Rider] {
		return errf(ErrRiderNotFound, "rider %q not registered", ne.ev.Rider)
	}
	if ne.revoked {
		return errf(ErrEventRevoked, "event %q already revoked", eventID)
	}
	if ne.appealed {
		return errf(ErrAlreadyAppealed, "event %q already appealed", eventID)
	}
	if m.satellite(st, ne) {
		return errf(ErrSatelliteEvent, "event %q is a satellite event in its cluster", eventID)
	}
	if opTs >= ne.ev.OccurAt+m.cfg.AppealWindow {
		return errf(ErrAppealWindowExpired, "appeal for event %q past window", eventID)
	}
	m.log = append(m.log, naiveOp{kind: nopFile, ts: opTs, id: id, eventID: eventID})
	return nil
}

// RuleAppeal 裁决申诉。
func (m *NaiveModel) RuleAppeal(opTs int64, appealID string, upheld bool) error {
	if err := m.checkClock(opTs); err != nil {
		return err
	}
	if appealID == "" {
		return errf(ErrInvalidParam, "appeal id is empty")
	}
	st := m.simulate(len(m.log) - 1)
	a, ok := st.appeals[appealID]
	if !ok {
		return errf(ErrAppealNotFound, "appeal %q not found", appealID)
	}
	if a.Ruled {
		return errf(ErrAppealRuled, "appeal %q already ruled", appealID)
	}
	m.log = append(m.log, naiveOp{kind: nopRule, ts: opTs, id: appealID,
		eventID: a.EventID, upheld: upheld})
	return nil
}

// QueryPeriod 查询某周期视图。
func (m *NaiveModel) QueryPeriod(opTs int64, rider string, period int) (PeriodReport, error) {
	if err := m.checkClock(opTs); err != nil {
		return PeriodReport{}, err
	}
	st := m.simulate(len(m.log) - 1)
	r, ok := st.rs[rider]
	if !ok {
		return PeriodReport{}, errf(ErrRiderNotFound, "rider %q not registered", rider)
	}
	// 查询作为已接受操作会触发首次结算，必须进入重放日志以保留冻结时点。
	m.log = append(m.log, naiveOp{kind: nopQuery, ts: opTs, id: rider, period: period})
	st = m.simulate(len(m.log) - 1)
	r = st.rs[rider]
	m.freezeThrough(r, opTs)
	st.maxTs = opTs
	if m.cfg.periodEnd(period) <= opTs {
		return PeriodReport{
			Period: period, Settled: true, Frozen: true,
			Score: r.scoreSnap[period], Grade: r.gradeSnap[period], Benefit: r.benefitSnap[period],
		}, nil
	}
	score := r.totals[period]
	return PeriodReport{
		Period: period, Score: score, Grade: m.cfg.gradeOf(score), Benefit: m.liveBenefit(r, period),
	}, nil
}

// Event 返回事件快照。
func (m *NaiveModel) Event(id string) (Event, bool) {
	st := m.simulate(len(m.log) - 1)
	ne, ok := st.events[id]
	if !ok {
		return Event{}, false
	}
	ev := ne.ev
	ev.Revoked = ne.revoked
	return ev, true
}

// Appeal 返回申诉快照。
func (m *NaiveModel) Appeal(id string) (Appeal, bool) {
	st := m.simulate(len(m.log) - 1)
	a, ok := st.appeals[id]
	if !ok {
		return Appeal{}, false
	}
	return *a, true
}

// Compensations 返回补偿账目。
func (m *NaiveModel) Compensations(rider string) []Compensation {
	st := m.simulate(len(m.log) - 1)
	r, ok := st.rs[rider]
	if !ok {
		return nil
	}
	out := make([]Compensation, len(r.comps))
	copy(out, r.comps)
	return out
}
