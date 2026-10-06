package bus

import (
	"fmt"
	"sort"
	"strings"
)

// OpKind 标识一次操作的类型。
type OpKind string

const (
	OpRegister OpKind = "RegisterDriver"
	OpBackup   OpKind = "AddBackup"
	OpSchedule OpKind = "ScheduleTrip"
	OpAlight   OpKind = "RegisterAlight"
	OpArrival  OpKind = "ReportArrival"
	OpHold     OpKind = "InterveneHold"
	OpSkip     OpKind = "InterveneSkip"
)

// Op 是一次可重放的操作；朴素模型与真实服务消费完全相同的操作序列。
type Op struct {
	Kind    OpKind
	Trip    int64
	Driver  int64
	Station int
	At      int64
}

// LogEntry 是一条操作日志：输入、输出与判定依据。
type LogEntry struct {
	Op     Op
	Err    error
	Detail string
}

func (e LogEntry) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s trip=%d driver=%d station=%d at=%d",
		e.Op.Kind, e.Op.Trip, e.Op.Driver, e.Op.Station, e.Op.At)
	if e.Err != nil {
		fmt.Fprintf(&b, " => ERROR %v", e.Err)
	} else {
		b.WriteString(" => OK")
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, " [%s]", e.Detail)
	}
	return b.String()
}

// Snapshot 是朴素模型与真实服务对照所用的完整状态。
type Snapshot struct {
	Clock int64
	Stops map[[2]int64]StopInfo
	Pool  []int64
	Trips []int64
}

// ---- 朴素模型：不使用 Service 的任何内部实现，
// 只用平铺 map 与逐车逐站推进的方式独立重放同一操作序列。----

type naiveRec struct {
	arrival   int64
	departure int64
	finalized bool
	reported  bool
	autoBorn  bool
	skipped   bool
	bunched   bool
	gap       bool
	predID    int64
	action    Action
	reason    Reason
	holdSec   int64
	insTrip   int64
}

type naiveTrip struct {
	id         int64
	driver     int64
	pos        int
	base       []int64
	holds      map[int]int64
	insertedAt int
	skipFrom   int
	skipTo     int
	skipUsed   bool
	dutyStart  int64
	lastReport int
	lastFinal  int
}

// NaiveModel 是独立编写的朴素重放模型。
type NaiveModel struct {
	cfg     Config
	control []bool

	drivers    map[int64]bool
	trips      map[int64]*naiveTrip
	pool       []int64
	poolDriver map[int64]int64
	order      []int64

	recs map[[2]int64]*naiveRec
	reqs map[[2]int64]bool

	clock    int64
	lastShed int64
	log      []LogEntry
}

// NewNaiveModel 构造朴素模型。
func NewNaiveModel(cfg Config) *NaiveModel {
	ctl := make([]bool, cfg.StationCount)
	for _, st := range cfg.ControlStations {
		ctl[st] = true
	}
	return &NaiveModel{
		cfg:        cfg,
		control:    ctl,
		drivers:    map[int64]bool{},
		trips:      map[int64]*naiveTrip{},
		poolDriver: map[int64]int64{},
		recs:       map[[2]int64]*naiveRec{},
		reqs:       map[[2]int64]bool{},
	}
}

// Log 返回累计操作日志。
func (m *NaiveModel) Log() []LogEntry { return m.log }

func (m *NaiveModel) planArrival(tr *naiveTrip, station int) int64 {
	if station < tr.insertedAt {
		return -1
	}
	if r, ok := m.recs[([2]int64{tr.id, int64(station)})]; ok && r.reported && !r.autoBorn {
		return r.arrival
	}
	anchor := tr.insertedAt
	anchorArrival := tr.base[anchor]
	for st := station - 1; st >= tr.insertedAt; st-- {
		if r, ok := m.recs[([2]int64{tr.id, int64(st)})]; ok && r.reported && !r.autoBorn {
			anchor = st
			anchorArrival = r.arrival
			break
		}
	}
	arrival := anchorArrival
	for st := anchor; st < station; st++ {
		if h, ok := tr.holds[st]; ok {
			arrival += h
		}
		if st != anchor && !m.isSkipped(tr, st) {
			arrival += m.cfg.Dwells[st]
		}
		arrival += m.cfg.TravelTimes[st]
	}
	return arrival
}

func (m *NaiveModel) isSkipped(tr *naiveTrip, station int) bool {
	return tr.skipUsed && station > tr.skipFrom && station < tr.skipTo
}

func (m *NaiveModel) planDeparture(tr *naiveTrip, station int, pa int64) int64 {
	if m.isSkipped(tr, station) {
		return pa
	}
	return pa + m.cfg.Dwells[station]
}

func (m *NaiveModel) nextControl(after int) int {
	for st := after + 1; st < m.cfg.StationCount; st++ {
		if m.control[st] {
			return st
		}
	}
	return -1
}

func (m *NaiveModel) prevAt(tr *naiveTrip, station int) *naiveTrip {
	for i := tr.pos - 1; i >= 0; i-- {
		key := [2]int64{m.order[i], int64(station)}
		if r, ok := m.recs[key]; ok && r.reported {
			return m.trips[m.order[i]]
		}
	}
	return nil
}

func (m *NaiveModel) effDep(tr *naiveTrip, station int) int64 {
	r := m.recs[([2]int64{tr.id, int64(station)})]
	if r != nil && r.finalized {
		return r.departure
	}
	pa := m.planArrival(tr, station)
	if r != nil && r.reported {
		pa = r.arrival
	}
	return m.planDeparture(tr, station, pa)
}

func (m *NaiveModel) appendLog(op Op, err error, detail string) {
	m.log = append(m.log, LogEntry{Op: op, Err: err, Detail: detail})
}

// Apply 逐条应用操作，返回（错误，判定依据描述）。
func (m *NaiveModel) Apply(op Op) (error, string) {
	var err error
	var detail string
	switch op.Kind {
	case OpRegister:
		err = m.applyRegister(op)
	case OpBackup:
		err = m.applyBackup(op)
	case OpSchedule:
		err, detail = m.applySchedule(op)
	case OpAlight:
		err = m.applyAlight(op)
	case OpArrival:
		err, detail = m.applyArrival(op)
	case OpHold:
		detail, err = m.applyIntervene(op, ActionHold)
	case OpSkip:
		detail, err = m.applyIntervene(op, ActionSkip)
	default:
		err = ErrInvalidArgument
	}
	m.appendLog(op, err, detail)
	return err, detail
}

func (m *NaiveModel) applyRegister(op Op) error {
	if op.Driver <= 0 {
		return ErrInvalidArgument
	}
	m.drivers[op.Driver] = true
	return nil
}

func (m *NaiveModel) applyBackup(op Op) error {
	if op.Trip <= 0 || op.Driver <= 0 || op.At < 0 {
		return ErrInvalidArgument
	}
	if op.At < m.clock {
		return ErrClockRollback
	}
	if _, ok := m.trips[op.Trip]; ok {
		return ErrInvalidArgument
	}
	if _, ok := m.poolDriver[op.Trip]; ok {
		return ErrInvalidArgument
	}
	if !m.drivers[op.Driver] {
		return ErrDriverNotFound
	}
	m.pool = append(m.pool, op.Trip)
	sort.Slice(m.pool, func(i, j int) bool { return m.pool[i] < m.pool[j] })
	m.poolDriver[op.Trip] = op.Driver
	m.clock = op.At
	return nil
}

func (m *NaiveModel) finalizeSkip(tr *naiveTrip, station int) {
	key := [2]int64{tr.id, int64(station)}
	if r, ok := m.recs[key]; ok {
		if !r.finalized {
			r.departure = r.arrival
			r.finalized = true
			tr.lastFinal = station
		}
		return
	}
	pa := m.planArrival(tr, station)
	m.recs[key] = &naiveRec{
		arrival: pa, departure: pa, finalized: true,
		reported: true, autoBorn: true, skipped: true, action: ActionSkip,
	}
	tr.lastFinal = station
}

func (m *NaiveModel) applyArrival(op Op) (error, string) {
	if op.Trip <= 0 || op.Station < 0 || op.At < 0 {
		return ErrInvalidArgument, ""
	}
	if op.At < m.clock {
		return ErrClockRollback, ""
	}
	tr, ok := m.trips[op.Trip]
	if !ok {
		return ErrTripNotFound, ""
	}
	if op.Station >= m.cfg.StationCount {
		return ErrStationNotFound, ""
	}
	if r, ok := m.recs[[2]int64{op.Trip, int64(op.Station)}]; ok && r.reported {
		return ErrDuplicateReport, ""
	}
	if op.Station <= tr.lastReport || op.Station < tr.insertedAt {
		return ErrOutOfOrder, ""
	}
	for st := tr.lastReport + 1; st < op.Station; st++ {
		if !m.isSkipped(tr, st) {
			return ErrOutOfOrder, ""
		}
	}
	for st := tr.lastReport + 1; st < op.Station; st++ {
		m.finalizeSkip(tr, st)
	}
	if tr.lastFinal < op.Station-1 {
		prev := op.Station - 1
		if pr, ok := m.recs[([2]int64{op.Trip, int64(prev)})]; ok {
			pa := m.planArrival(tr, prev)
			pr.departure = m.planDeparture(tr, prev, pa)
			pr.finalized = true
			tr.lastFinal = prev
		}
	}

	pa := m.planArrival(tr, op.Station)
	key := [2]int64{op.Trip, int64(op.Station)}
	r, exists := m.recs[key]
	if !exists {
		r = &naiveRec{}
		m.recs[key] = r
	}
	r.arrival = op.At
	r.departure = m.planDeparture(tr, op.Station, pa)
	r.reported = true
	tr.lastReport = op.Station
	m.clock = op.At

	detail := ""
	if m.control[op.Station] {
		detail = m.evaluate(tr, op.Station, r)
	}
	return nil, detail
}

func (m *NaiveModel) evaluate(tr *naiveTrip, station int, r *naiveRec) string {
	pred := m.prevAt(tr, station)
	if pred == nil {
		return "控制站：无前车到站记录，仅记录到站"
	}
	pr := m.recs[([2]int64{pred.id, int64(station)})]
	gap := r.arrival - pr.arrival
	r.predID = pred.id
	h := m.cfg.TargetHeadway
	switch {
	case gap*2 < h:
		r.bunched = true
		return fmt.Sprintf("串车：间隔 %d 严格小于 h/2=%.1f（前车车次 %d）", gap, float64(h)/2, pred.id)
	case gap > 2*h:
		r.gap = true
		return m.tryInsert(tr, pred, station, r, pr.arrival)
	default:
		return fmt.Sprintf("正常：间隔 %d 落在 [h/2,2h]（前车车次 %d）", gap, pred.id)
	}
}

func (m *NaiveModel) tryInsert(tr, pred *naiveTrip, station int, r *naiveRec, predArrival int64) string {
	if len(m.pool) == 0 {
		r.action = ActionNone
		r.reason = ReasonPoolEmpty
		return "大间隔：备车池为空，仅记录事件"
	}
	id := m.pool[0]
	m.pool = m.pool[1:]
	driver := m.poolDriver[id]
	delete(m.poolDriver, id)

	n := m.cfg.StationCount
	base := make([]int64, n)
	for i := range base {
		base[i] = -1
	}
	insertArrival := predArrival + m.cfg.TargetHeadway
	if insertArrival < m.clock {
		insertArrival = m.clock
	}
	acc := insertArrival
	for st := station; st < n; st++ {
		base[st] = acc
		if st < n-1 {
			acc += m.cfg.Dwells[st] + m.cfg.TravelTimes[st]
		}
	}
	ntr := &naiveTrip{
		id:         id,
		driver:     driver,
		pos:        tr.pos,
		base:       base,
		holds:      map[int]int64{},
		insertedAt: station,
		skipFrom:   -1,
		skipTo:     -1,
		dutyStart:  insertArrival,
		lastReport: station,
		lastFinal:  station - 1,
	}
	// 把插入车次前移到后车 tr 之前，其余车次顺移。
	m.order = append(m.order, 0)
	copy(m.order[tr.pos+1:], m.order[tr.pos:])
	m.order[tr.pos] = id
	for i := tr.pos; i < len(m.order); i++ {
		if t, ok := m.trips[m.order[i]]; ok {
			t.pos = i
		}
	}
	m.trips[id] = ntr
	m.recs[([2]int64{id, int64(station)})] = &naiveRec{
		arrival:   insertArrival,
		departure: insertArrival + m.cfg.Dwells[station],
		finalized: true,
		reported:  true,
		autoBorn:  true,
		action:    ActionInsert,
	}
	r.action = ActionInsert
	r.insTrip = id
	return fmt.Sprintf("大间隔：插入最小备车车次 %d，到站 %d", id, insertArrival)
}

func (m *NaiveModel) applyIntervene(op Op, kind Action) (string, error) {
	if op.Trip <= 0 || op.Station < 0 {
		return "", ErrInvalidArgument
	}
	tr, ok := m.trips[op.Trip]
	if !ok {
		return "", ErrTripNotFound
	}
	if op.Station >= m.cfg.StationCount {
		return "", ErrStationNotFound
	}
	r, exists := m.recs[([2]int64{op.Trip, int64(op.Station)})]
	if !exists || !r.reported || !m.control[op.Station] {
		return "", ErrNotBunched
	}
	if op.Station < tr.lastReport || r.finalized {
		return "", ErrOutOfOrder
	}
	if !r.bunched {
		return "", ErrNotBunched
	}
	if kind == ActionHold {
		return m.applyHold(tr, op.Station, r), nil
	}
	return m.applySkip(tr, op.Station, r), nil
}

func (m *NaiveModel) applyHold(tr *naiveTrip, station int, r *naiveRec) string {
	plannedArrival := m.planArrival(tr, station)
	plannedDeparture := m.planDeparture(tr, station, plannedArrival)
	target := plannedDeparture
	if pred := m.prevAt(tr, station); pred != nil {
		target = m.effDep(pred, station) + m.cfg.TargetHeadway
	}
	want := target - plannedDeparture
	if want <= 0 {
		r.action = ActionNone
		return "串车但前车离站已满足目标间隔，无需扣车"
	}
	hold := want
	capped := false
	if hold >= m.cfg.HoldCap {
		hold = m.cfg.HoldCap
		capped = true
	}
	if plannedDeparture+hold-tr.dutyStart > m.cfg.MaxDutySeconds {
		r.action = ActionNone
		r.reason = ReasonDutyLimit
		return "扣车会使连续在岗超上限，拒绝并降级为不干预"
	}
	departure := plannedDeparture + hold
	latest := plannedArrival + m.cfg.DepartureTolerance
	if departure > latest {
		return m.applySkip(tr, station, r) + "（扣车超最晚离站，改判跳站）"
	}
	r.action = ActionHold
	r.holdSec = hold
	r.departure = departure
	r.finalized = true
	tr.lastFinal = station
	tr.holds[station] += hold
	if capped {
		r.reason = ReasonHoldCap
		return fmt.Sprintf("扣车 %d 秒（需求 %d 秒超过扣车上限 %d，只扣到上限）",
			hold, want, m.cfg.HoldCap)
	}
	return fmt.Sprintf("扣车 %d 秒，离站 %d", hold, departure)
}

func (m *NaiveModel) applySkip(tr *naiveTrip, station int, r *naiveRec) string {
	if tr.skipUsed {
		r.action = ActionNone
		r.reason = ReasonSkipUsed
		return "本车次已使用过一次跳站，拒绝"
	}
	to := m.nextControl(station)
	if to < 0 {
		r.action = ActionNone
		r.reason = ReasonNoNextControl
		return "后方无控制站，无法跳站"
	}
	for st := station + 1; st < to; st++ {
		if m.reqs[([2]int64{tr.id, int64(st)})] {
			r.action = ActionNone
			r.reason = ReasonAlightRequest
			return fmt.Sprintf("拟跳过的站 %d 存在下车请求，跳站被拒", st)
		}
	}
	tr.skipUsed = true
	tr.skipFrom = station
	tr.skipTo = to
	plannedArrival := m.planArrival(tr, station)
	r.action = ActionSkip
	r.departure = m.planDeparture(tr, station, plannedArrival)
	r.finalized = true
	tr.lastFinal = station
	for st := station + 1; st < to; st++ {
		m.finalizeSkip(tr, st)
	}
	return fmt.Sprintf("跳站：跳过站 %d..%d 的非控制站，到下一个控制站 %d",
		station+1, to-1, to)
}

// Snapshot 导出朴素模型当前完整状态，供与 Service 逐条对照。
func (m *NaiveModel) Snapshot() Snapshot {
	snap := Snapshot{
		Clock: m.clock,
		Stops: map[[2]int64]StopInfo{},
		Pool:  append([]int64(nil), m.pool...),
		Trips: append([]int64(nil), m.order...),
	}
	for key, r := range m.recs {
		tr := m.trips[key[0]]
		info := StopInfo{
			TripID:   key[0],
			Station:  int(key[1]),
			Arrival:  r.arrival,
			Reported: r.reported,
			Skipped:  r.skipped,
			Action:   r.action,
			Reason:   r.reason,
			HoldSec:  r.holdSec,
			Inserted: r.insTrip,
			PrevTrip: r.predID,
		}
		if r.insTrip == 0 {
			info.Inserted = -1
		}
		if r.finalized {
			info.Departure = r.departure
			info.Finalized = true
		} else {
			info.Departure = m.planDeparture(tr, int(key[1]), m.planArrival(tr, int(key[1])))
		}
		if m.control[int(key[1])] {
			switch {
			case r.bunched:
				info.Verdict = VerdictBunch
			case r.gap:
				info.Verdict = VerdictGap
			case r.reported:
				info.Verdict = VerdictNormal
			}
		}
		snap.Stops[key] = info
	}
	return snap
}

func (m *NaiveModel) applySchedule(op Op) (error, string) {
	if op.Trip <= 0 || op.Driver <= 0 || op.At < 0 {
		return ErrInvalidArgument, ""
	}
	if op.At < m.clock {
		return ErrClockRollback, ""
	}
	if _, ok := m.trips[op.Trip]; ok {
		return ErrInvalidArgument, ""
	}
	if _, ok := m.poolDriver[op.Trip]; ok {
		return ErrInvalidArgument, ""
	}
	if !m.drivers[op.Driver] {
		return ErrDriverNotFound, ""
	}
	if op.Trip <= m.lastShed {
		return ErrInvalidArgument, ""
	}
	n := m.cfg.StationCount
	base := make([]int64, n)
	for st := 1; st < n; st++ {
		base[st] = base[st-1] + m.cfg.Dwells[st-1] + m.cfg.TravelTimes[st-1]
	}
	tr := &naiveTrip{
		id:         op.Trip,
		driver:     op.Driver,
		pos:        len(m.order),
		base:       base,
		holds:      map[int]int64{},
		insertedAt: 0,
		skipFrom:   -1,
		skipTo:     -1,
		dutyStart:  op.At,
		lastReport: 0,
		lastFinal:  -1,
	}
	m.trips[op.Trip] = tr
	m.order = append(m.order, op.Trip)
	m.lastShed = op.Trip
	m.recs[[2]int64{op.Trip, 0}] = &naiveRec{
		arrival: op.At, departure: op.At + m.cfg.Dwells[0],
		finalized: true, reported: true,
	}
	tr.lastFinal = 0
	m.clock = op.At
	return nil, "首发投放，站0离站冻结"
}

func (m *NaiveModel) applyAlight(op Op) error {
	if op.Trip <= 0 || op.Station < 0 || op.At < 0 {
		return ErrInvalidArgument
	}
	if op.At < m.clock {
		return ErrClockRollback
	}
	tr, ok := m.trips[op.Trip]
	if !ok {
		return ErrTripNotFound
	}
	if op.Station >= m.cfg.StationCount {
		return ErrStationNotFound
	}
	if op.Station < tr.insertedAt {
		return ErrOutOfOrder
	}
	m.reqs[[2]int64{op.Trip, int64(op.Station)}] = true
	m.clock = op.At
	return nil
}
