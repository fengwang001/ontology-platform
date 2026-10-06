package bus

import "fmt"

// OpKind 标识朴素模型可重放的操作。
type OpKind int

const (
	OpRegisterDriver OpKind = iota + 1
	OpRegisterTrip
	OpAddSpare
	OpRegisterAlighting
	OpReportArrival
)

// Op 是一次操作的完整输入。
type Op struct {
	Kind   OpKind
	TripID int64
	Stop   int
	At     int64
	Driver string
}

// NaiveEvent 是朴素模型逐车逐站推进得到的单站结果。
type NaiveEvent struct {
	Reported     bool
	Arrival      int64
	Departure    int64
	Intervention Intervention
	Bunched      bool
	Reason       string
}

type naiveTrip struct {
	id        int64
	anchor    int64
	dutyStart int64
	delay     int64
	next      int
	entry     int
	skipUsed  bool
	events    []NaiveEvent
	alighting []bool
}

// NaiveModel 是独立编写的参照实现：不调用 Service 的任何方法，
// 用最直白的顺序表与逐站推进复算结果。
type NaiveModel struct {
	scheme  Scheme
	clock   int64
	drivers map[string]bool
	trips   map[int64]*naiveTrip
	order   []int64
	spares  []int64
	spareDr map[int64]string
	lastReg int64
}

// NewNaiveModel 构造朴素模型。
func NewNaiveModel(sc Scheme) *NaiveModel {
	return &NaiveModel{
		scheme:  sc,
		clock:   -1,
		drivers: map[string]bool{},
		trips:   map[int64]*naiveTrip{},
		spareDr: map[int64]string{},
		lastReg: -(1 << 62),
	}
}

func (m *NaiveModel) newTrip(id int64) *naiveTrip {
	n := len(m.scheme.Stops)
	return &naiveTrip{
		id: id, anchor: 0, next: 0, entry: 0,
		events:    make([]NaiveEvent, n),
		alighting: make([]bool, n),
	}
}

// Apply 重放一条操作；被拒绝时返回与 Service 同类别的错误且不改变状态。
func (m *NaiveModel) Apply(op Op) (*NaiveEvent, ErrorKind) {
	if op.At < 0 {
		return nil, ErrInvalidParam
	}
	if op.At < m.clock {
		return nil, ErrClockRollback
	}
	switch op.Kind {
	case OpRegisterDriver:
		if op.Driver == "" {
			return nil, ErrInvalidParam
		}
		if !m.drivers[op.Driver] {
			m.drivers[op.Driver] = true
			m.clock = op.At
		}
		return nil, 0
	case OpRegisterTrip:
		if op.TripID <= 0 || op.Driver == "" {
			return nil, ErrInvalidParam
		}
		if _, ex := m.trips[op.TripID]; ex || op.TripID <= m.lastReg {
			return nil, ErrInvalidParam
		}
		if !m.drivers[op.Driver] {
			return nil, ErrDriverNotFound
		}
		m.trips[op.TripID] = m.newTrip(op.TripID)
		m.order = append(m.order, op.TripID)
		m.lastReg = op.TripID
		m.clock = op.At
		return nil, 0
	case OpAddSpare:
		if op.TripID <= 0 || op.Driver == "" {
			return nil, ErrInvalidParam
		}
		if _, ex := m.spareDr[op.TripID]; ex {
			return nil, ErrInvalidParam
		}
		if _, ex := m.trips[op.TripID]; ex {
			return nil, ErrInvalidParam
		}
		if !m.drivers[op.Driver] {
			return nil, ErrDriverNotFound
		}
		m.spareDr[op.TripID] = op.Driver
		m.spares = append(m.spares, op.TripID)
		m.clock = op.At
		return nil, 0
	case OpRegisterAlighting:
		tr, k := m.find(op.TripID)
		if k != 0 {
			return nil, k
		}
		if op.Stop < 0 || op.Stop >= len(m.scheme.Stops) {
			return nil, ErrStopNotFound
		}
		if tr.events[op.Stop].Reported {
			return nil, ErrOutOfOrder
		}
		tr.alighting[op.Stop] = true
		m.clock = op.At
		return nil, 0
	case OpReportArrival:
		tr, k := m.find(op.TripID)
		if k != 0 {
			return nil, k
		}
		if op.Stop < 0 || op.Stop >= len(m.scheme.Stops) {
			return nil, ErrStopNotFound
		}
		if op.Stop < tr.next {
			return nil, ErrDuplicateReport
		}
		if op.Stop > tr.next {
			return nil, ErrOutOfOrder
		}
		m.clock = op.At
		ev := m.arrive(tr, op.Stop, op.At)
		cp := ev
		return &cp, 0
	default:
		return nil, ErrInvalidParam
	}
}

func (m *NaiveModel) find(id int64) (*naiveTrip, ErrorKind) {
	tr, ok := m.trips[id]
	if !ok {
		return nil, ErrTripNotFound
	}
	return tr, 0
}

func (m *NaiveModel) indexOf(id int64) int {
	for i, x := range m.order {
		if x == id {
			return i
		}
	}
	return -1
}

func (m *NaiveModel) prevAt(tr *naiveTrip, stop int) *naiveTrip {
	for j := m.indexOf(tr.id) - 1; j >= 0; j-- {
		p := m.trips[m.order[j]]
		if p.entry <= stop && p.events[stop].Reported {
			return p
		}
	}
	return nil
}

func (m *NaiveModel) arrive(tr *naiveTrip, stop int, at int64) NaiveEvent {
	sc := m.scheme
	if tr.anchor == 0 {
		tr.anchor = at
	}
	if tr.dutyStart == 0 {
		tr.dutyStart = at
	}
	tr.events[stop] = NaiveEvent{Reported: true, Arrival: at}
	tr.next = stop + 1
	if !sc.IsControl(stop) {
		ev := NaiveEvent{Reported: true, Arrival: at, Departure: at + sc.Stops[stop].DwellSec,
			Reason: "non-control"}
		tr.events[stop] = ev
		return ev
	}
	prev := m.prevAt(tr, stop)
	if prev == nil {
		ev := NaiveEvent{Reported: true, Arrival: at, Departure: at + sc.Stops[stop].DwellSec,
			Reason: "first"}
		tr.events[stop] = ev
		return ev
	}
	gap := at - prev.events[stop].Arrival
	switch {
	case gap > 2*sc.HeadwaySec:
		return m.bigGap(tr, prev, stop, at, gap)
	case gap*2 < sc.HeadwaySec:
		return m.bunched(tr, prev, stop, at, gap)
	default:
		ev := NaiveEvent{Reported: true, Arrival: at, Departure: at + sc.Stops[stop].DwellSec,
			Reason: "normal-gap"}
		tr.events[stop] = ev
		return ev
	}
}

func (m *NaiveModel) bigGap(tr, prev *naiveTrip, stop int, at, gap int64) NaiveEvent {
	sc := m.scheme
	if len(m.spares) == 0 {
		ev := NaiveEvent{Reported: true, Arrival: at, Departure: at + sc.Stops[stop].DwellSec,
			Reason: "big-gap-empty"}
		tr.events[stop] = ev
		return ev
	}
	// 取车次号最小的备车。
	mini := 0
	for i := 1; i < len(m.spares); i++ {
		if m.spares[i] < m.spares[mini] {
			mini = i
		}
	}
	sp := m.spares[mini]
	m.spares = append(m.spares[:mini], m.spares[mini+1:]...)

	idx := m.indexOf(tr.id)
	lo := prev.id
	hi := tr.id
	newID := sp
	if !(newID > lo && newID < hi) {
		newID = lo + (hi-lo)/2
		if newID <= lo {
			newID = lo + 1
		}
		for id := lo + 1; id < hi; id++ {
			if _, used := m.trips[id]; used {
				continue
			}
			inPool := false
			for _, p := range m.spares {
				if p == id && p != sp {
					inPool = true
				}
			}
			if !inPool {
				newID = id
				break
			}
		}
	}
	st := m.newTrip(newID)
	st.entry = stop
	st.next = stop + 1
	target := prev.events[stop].Arrival + sc.HeadwaySec
	st.dutyStart = target
	st.anchor = target
	for i := 0; i < stop; i++ {
		st.anchor -= sc.Stops[i].DwellSec + sc.Travel[i]
	}
	st.events[stop] = NaiveEvent{
		Reported: true, Arrival: target, Departure: target + sc.Stops[stop].DwellSec,
		Intervention: InterventionSpareInsert, Reason: "inserted",
	}
	m.trips[newID] = st
	m.order = append(m.order[:idx], append([]int64{newID}, m.order[idx:]...)...)

	ev := NaiveEvent{Reported: true, Arrival: at, Departure: at + sc.Stops[stop].DwellSec,
		Reason: fmt.Sprintf("big-gap-insert-%d", newID)}
	tr.events[stop] = ev
	return ev
}

func (m *NaiveModel) bunched(tr, prev *naiveTrip, stop int, at, gap int64) NaiveEvent {
	sc := m.scheme
	want := prev.events[stop].Departure + sc.HeadwaySec
	hold := want - (at + sc.Stops[stop].DwellSec)
	if hold < 0 {
		hold = 0
	}
	capped := hold
	if capped > sc.HoldCapSec {
		capped = sc.HoldCapSec
	}
	planned := sc.planArrival(tr.anchor, stop) + tr.delay
	latest := planned + sc.ToleranceSec
	tooLate := func(h int64) bool { return at+sc.Stops[stop].DwellSec+h > latest }

	if hold > sc.HoldCapSec && tooLate(capped) {
		return m.skipOrRecord(tr, stop, at, gap, "hold-cap-and-late")
	}
	if hold <= sc.HoldCapSec && tooLate(hold) {
		return m.skipOrRecord(tr, stop, at, gap, "hold-late")
	}
	h := hold
	if hold > sc.HoldCapSec {
		h = capped
	}
	if m.dutyOverflow(tr, at+sc.Stops[stop].DwellSec+h) {
		ev := NaiveEvent{
			Reported: true, Bunched: true, Arrival: at,
			Departure: at + sc.Stops[stop].DwellSec, Reason: "duty-reject",
		}
		tr.events[stop] = ev
		return ev
	}
	dep := at + sc.Stops[stop].DwellSec + h
	tr.delay += h
	ev := NaiveEvent{
		Reported: true, Bunched: true, Arrival: at, Departure: dep,
		Intervention: InterventionHold, Reason: fmt.Sprintf("hold-%d", h),
	}
	tr.events[stop] = ev
	return ev
}

func (m *NaiveModel) dutyOverflow(tr *naiveTrip, end int64) bool {
	return end-tr.dutyStart > m.scheme.DutyCapSec
}

func (m *NaiveModel) skipOrRecord(tr *naiveTrip, stop int, at, gap int64, why string) NaiveEvent {
	sc := m.scheme
	next := -1
	for i := stop + 1; i < len(sc.Stops); i++ {
		if sc.IsControl(i) {
			next = i
			break
		}
	}
	blocked := tr.skipUsed || next < 0
	for k := stop + 1; k < next && !blocked; k++ {
		if tr.alighting[k] {
			blocked = true
		}
	}
	if blocked {
		ev := NaiveEvent{
			Reported: true, Bunched: true, Arrival: at,
			Departure: at + sc.Stops[stop].DwellSec, Reason: why + "-record",
		}
		tr.events[stop] = ev
		return ev
	}
	tr.skipUsed = true
	tr.events[stop] = NaiveEvent{
		Reported: true, Bunched: true, Arrival: at, Departure: at,
		Intervention: InterventionSkip, Reason: why + "-skip",
	}
	t := at
	for k := stop + 1; k < next; k++ {
		t += sc.Travel[k-1]
		tr.events[k] = NaiveEvent{
			Reported: true, Bunched: true, Arrival: t, Departure: t,
			Intervention: InterventionSkip, Reason: "skipped",
		}
		tr.next = k + 1
	}
	return tr.events[stop]
}

// Event 返回朴素模型中某车次某站的结果。
func (m *NaiveModel) Event(tripID int64, stop int) (NaiveEvent, ErrorKind) {
	tr, k := m.find(tripID)
	if k != 0 {
		return NaiveEvent{}, k
	}
	if stop < 0 || stop >= len(m.scheme.Stops) {
		return NaiveEvent{}, ErrStopNotFound
	}
	return tr.events[stop], 0
}

// Order 返回朴素模型当前发车次序。
func (m *NaiveModel) Order() []int64 {
	out := make([]int64, len(m.order))
	copy(out, m.order)
	return out
}
