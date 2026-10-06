package bus

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// Intervention 表示站点上实际生效的干预类别。
type Intervention int

const (
	InterventionNone Intervention = iota
	InterventionHold
	InterventionSkip
	InterventionSpareInsert
)

func interventionName(i Intervention) string {
	switch i {
	case InterventionHold:
		return "HOLD"
	case InterventionSkip:
		return "SKIP"
	case InterventionSpareInsert:
		return "SPARE_INSERT"
	default:
		return "NONE"
	}
}

// StopEvent 是某车次在某站的到离记录。
type StopEvent struct {
	reported     bool
	Arrival      int64
	Departure    int64
	Intervention Intervention
	Reason       string
	bunched      bool
}

// EventView 是查询返回的值拷贝。
type EventView struct {
	Reported     bool
	Arrival      int64
	Departure    int64
	Intervention Intervention
	Reason       string
}

func viewOf(e *StopEvent) EventView {
	return EventView{
		Reported:     e.reported,
		Arrival:      e.Arrival,
		Departure:    e.Departure,
		Intervention: e.Intervention,
		Reason:       e.Reason,
	}
}

type spareInfo struct {
	id     int64
	driver string
}

type trip struct {
	id        int64
	driver    string
	anchor    int64
	delay     int64
	nextStop  int
	entryStop int
	events    []StopEvent
	alighting []bool
	skipUsed  bool
	spare     bool
	dutyStart int64
}

// Service 是车辆排班与串车调整服务，单把互斥锁保证并发等价于某一串行顺序。
type Service struct {
	mu      sync.Mutex
	scheme  Scheme
	clock   int64
	trips   map[int64]*trip
	order   []int64
	drivers map[string]bool
	spares  []spareInfo
	lastReg int64
	logger  io.Writer
	logSeq  int64
}

// NewService 依据方案构造服务。
func NewService(sc Scheme) (*Service, error) {
	if err := sc.validate(); err != nil {
		return nil, err
	}
	return &Service{
		scheme:  sc,
		trips:   map[int64]*trip{},
		drivers: map[string]bool{},
		clock:   -1,
		lastReg: -(1 << 62),
	}, nil
}

// SetLogger 打开操作日志：每条操作的输入、输出与判定依据都会写入 w。
func (s *Service) SetLogger(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = w
}

func (s *Service) logf(format string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logSeq++
	fmt.Fprintf(s.logger, "#%d %s\n", s.logSeq, fmt.Sprintf(format, args...))
}

// Clock 返回最近一次被接受操作的时刻。
func (s *Service) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// Order 返回当前车次发车次序的快照（含已插入备车）。
func (s *Service) Order() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, len(s.order))
	copy(out, s.order)
	return out
}

func newTrip(id int64, driver string, spare bool, stops int) *trip {
	return &trip{
		id:        id,
		driver:    driver,
		spare:     spare,
		nextStop:  0,
		entryStop: 0,
		events:    make([]StopEvent, stops),
		alighting: make([]bool, stops),
	}
}

func (s *Service) lookupTrip(id int64) (*trip, error) {
	tr, ok := s.trips[id]
	if !ok {
		return nil, kindError(ErrTripNotFound, fmt.Sprintf("trip %d not found", id))
	}
	return tr, nil
}

func (s *Service) checkStop(stop int) error {
	if stop < 0 || stop >= len(s.scheme.Stops) {
		return kindError(ErrStopNotFound, fmt.Sprintf("stop index %d out of range", stop))
	}
	return nil
}

// RegisterDriver 登记一名司机，必须早于绑定该车次的任何操作。
func (s *Service) RegisterDriver(driverID string, atSec int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if driverID == "" || atSec < 0 {
		return kindError(ErrInvalidParam, "driver id must be non-empty and timestamp non-negative")
	}
	if atSec < s.clock {
		s.logf("RegisterDriver(%q,%d) -> CLOCK_ROLLBACK clock=%d", driverID, atSec, s.clock)
		return kindError(ErrClockRollback, fmt.Sprintf("t=%d < clock=%d", atSec, s.clock))
	}
	if !s.drivers[driverID] {
		s.drivers[driverID] = true
		s.clock = atSec
	}
	s.logf("RegisterDriver(%q,%d) -> ok", driverID, atSec)
	return nil
}

// RegisterTrip 登记一个常规车次，车次号必须严格递增；车次号先后即计划发车次序。
func (s *Service) RegisterTrip(tripID int64, driverID string, atSec int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tripID <= 0 || atSec < 0 || driverID == "" {
		return kindError(ErrInvalidParam, "tripID/driverID invalid or timestamp negative")
	}
	if atSec < s.clock {
		s.logf("RegisterTrip(%d,%q,%d) -> CLOCK_ROLLBACK clock=%d", tripID, driverID, atSec, s.clock)
		return kindError(ErrClockRollback, fmt.Sprintf("t=%d < clock=%d", atSec, s.clock))
	}
	if _, exists := s.trips[tripID]; exists {
		return kindError(ErrInvalidParam, fmt.Sprintf("trip %d already registered", tripID))
	}
	if tripID <= s.lastReg {
		return kindError(ErrInvalidParam, fmt.Sprintf("trip %d out of registration order (last=%d)", tripID, s.lastReg))
	}
	if !s.drivers[driverID] {
		err := kindError(ErrDriverNotFound, "driver not registered: "+driverID)
		s.logf("RegisterTrip(%d,%q,%d) -> %v", tripID, driverID, atSec, err)
		return err
	}
	s.trips[tripID] = newTrip(tripID, driverID, false, len(s.scheme.Stops))
	s.order = append(s.order, tripID)
	s.lastReg = tripID
	s.clock = atSec
	s.logf("RegisterTrip(%d,driver=%q,%d) -> ok order=%v", tripID, driverID, atSec, s.order)
	return nil
}

// AddSpare 向备车池加入一辆备车。
func (s *Service) AddSpare(spareID int64, driverID string, atSec int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if spareID <= 0 || atSec < 0 || driverID == "" {
		return kindError(ErrInvalidParam, "spareID/driverID invalid or timestamp negative")
	}
	if atSec < s.clock {
		s.logf("AddSpare(%d,%q,%d) -> CLOCK_ROLLBACK clock=%d", spareID, driverID, atSec, s.clock)
		return kindError(ErrClockRollback, fmt.Sprintf("t=%d < clock=%d", atSec, s.clock))
	}
	for _, sp := range s.spares {
		if sp.id == spareID {
			return kindError(ErrInvalidParam, fmt.Sprintf("spare %d already in pool", spareID))
		}
	}
	if _, exists := s.trips[spareID]; exists {
		return kindError(ErrInvalidParam, fmt.Sprintf("id %d already used by a trip", spareID))
	}
	if !s.drivers[driverID] {
		err := kindError(ErrDriverNotFound, "driver not registered: "+driverID)
		s.logf("AddSpare(%d,%q,%d) -> %v", spareID, driverID, atSec, err)
		return err
	}
	s.spares = append(s.spares, spareInfo{id: spareID, driver: driverID})
	sort.Slice(s.spares, func(i, j int) bool { return s.spares[i].id < s.spares[j].id })
	s.clock = atSec
	s.logf("AddSpare(%d,driver=%q,%d) -> ok poolSize=%d", spareID, driverID, atSec, len(s.spares))
	return nil
}

// RegisterAlighting 在某车次某站登记下车请求（必须发生在该车到该站之前）。
func (s *Service) RegisterAlighting(tripID int64, stop int, atSec int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if atSec < 0 {
		return kindError(ErrInvalidParam, "negative timestamp")
	}
	if atSec < s.clock {
		s.logf("RegisterAlighting(%d,stop=%d,%d) -> CLOCK_ROLLBACK", tripID, stop, atSec)
		return kindError(ErrClockRollback, fmt.Sprintf("t=%d < clock=%d", atSec, s.clock))
	}
	tr, err := s.lookupTrip(tripID)
	if err != nil {
		s.logf("RegisterAlighting(%d,stop=%d,%d) -> %v", tripID, stop, atSec, err)
		return err
	}
	if err := s.checkStop(stop); err != nil {
		s.logf("RegisterAlighting(%d,stop=%d,%d) -> %v", tripID, stop, atSec, err)
		return err
	}
	if tr.events[stop].reported {
		err := kindError(ErrOutOfOrder, fmt.Sprintf("trip %d already passed stop %d", tripID, stop))
		s.logf("RegisterAlighting(%d,stop=%d,%d) -> %v", tripID, stop, atSec, err)
		return err
	}
	tr.alighting[stop] = true
	s.clock = atSec
	s.logf("RegisterAlighting(trip=%d,stop=%d,t=%d) -> ok", tripID, stop, atSec)
	return nil
}

// ReportArrival 上报车次到达站点；控制站到站瞬间完成串车判定与干预。
func (s *Service) ReportArrival(tripID int64, stop int, atSec int64) (*EventView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if atSec < 0 {
		return nil, kindError(ErrInvalidParam, "negative timestamp")
	}
	if atSec < s.clock {
		s.logf("ReportArrival(%d,stop=%d,%d) -> CLOCK_ROLLBACK clock=%d", tripID, stop, atSec, s.clock)
		return nil, kindError(ErrClockRollback, fmt.Sprintf("t=%d < clock=%d", atSec, s.clock))
	}
	tr, err := s.lookupTrip(tripID)
	if err != nil {
		s.logf("ReportArrival(%d,stop=%d,%d) -> %v", tripID, stop, atSec, err)
		return nil, err
	}
	if err := s.checkStop(stop); err != nil {
		s.logf("ReportArrival(%d,stop=%d,%d) -> %v", tripID, stop, atSec, err)
		return nil, err
	}
	if stop < tr.nextStop {
		err := kindError(ErrDuplicateReport, fmt.Sprintf("trip %d already reported stop %d", tripID, stop))
		s.logf("ReportArrival(%d,stop=%d,%d) -> %v", tripID, stop, atSec, err)
		return nil, err
	}
	if stop > tr.nextStop {
		err := kindError(ErrOutOfOrder, fmt.Sprintf("trip %d expects stop %d, got %d", tripID, tr.nextStop, stop))
		s.logf("ReportArrival(%d,stop=%d,%d) -> %v", tripID, stop, atSec, err)
		return nil, err
	}

	s.clock = atSec
	ev := s.acceptArrival(tr, stop, atSec)
	s.logf("ReportArrival(trip=%d,stop=%d,t=%d) -> arr=%d dep=%d kind=%s bunched=%v reason=%q",
		tripID, stop, atSec, ev.Arrival, ev.Departure, interventionName(ev.Intervention), ev.bunched, ev.Reason)
	v := viewOf(&ev)
	return &v, nil
}

// RequestIntervention 查询某串车到站最终生效的干预；对象非串车返回 ErrNotBunched。
func (s *Service) RequestIntervention(tripID int64, stop int, atSec int64) (Intervention, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if atSec < 0 {
		return InterventionNone, "", kindError(ErrInvalidParam, "negative timestamp")
	}
	if atSec < s.clock {
		return InterventionNone, "", kindError(ErrClockRollback, fmt.Sprintf("t=%d < clock=%d", atSec, s.clock))
	}
	tr, err := s.lookupTrip(tripID)
	if err != nil {
		return InterventionNone, "", err
	}
	if err := s.checkStop(stop); err != nil {
		return InterventionNone, "", err
	}
	ev := &tr.events[stop]
	if !ev.reported {
		return InterventionNone, "", kindError(ErrOutOfOrder, fmt.Sprintf("trip %d has not reported stop %d", tripID, stop))
	}
	if !ev.bunched {
		s.logf("RequestIntervention(%d,stop=%d) -> NOT_BUNCHED", tripID, stop)
		return InterventionNone, "", kindError(ErrNotBunched, fmt.Sprintf("trip %d at stop %d is not bunched", tripID, stop))
	}
	s.clock = atSec
	s.logf("RequestIntervention(%d,stop=%d) -> %s reason=%q", tripID, stop, interventionName(ev.Intervention), ev.Reason)
	return ev.Intervention, ev.Reason, nil
}

// GetEvent O(1) 查询某车次某站的到离时刻与干预类别。
func (s *Service) GetEvent(tripID int64, stop int) (EventView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr, err := s.lookupTrip(tripID)
	if err != nil {
		return EventView{}, err
	}
	if err := s.checkStop(stop); err != nil {
		return EventView{}, err
	}
	return viewOf(&tr.events[stop]), nil
}

func (s *Service) tripIndex(tr *trip) int {
	for i, id := range s.order {
		if id == tr.id {
			return i
		}
	}
	return -1
}

// prevTripAt 返回运行次序上 tr 之前、最近一辆在 stop 已上报到站的车。
func (s *Service) prevTripAt(tr *trip, stop int) *trip {
	idx := s.tripIndex(tr)
	for j := idx - 1; j >= 0; j-- {
		p := s.trips[s.order[j]]
		if p.entryStop > stop {
			continue
		}
		if p.events[stop].reported {
			return p
		}
	}
	return nil
}

// acceptArrival 在前置校验全部通过后执行状态迁移。
func (s *Service) acceptArrival(tr *trip, stop int, atSec int64) StopEvent {
	if tr.dutyStart == 0 {
		tr.dutyStart = atSec
		tr.anchor = atSec
	}
	tr.events[stop] = StopEvent{reported: true, Arrival: atSec}
	tr.nextStop = stop + 1

	sc := s.scheme
	if !sc.IsControl(stop) {
		dep := atSec + sc.Stops[stop].DwellSec
		tr.events[stop] = StopEvent{
			reported: true, Arrival: atSec, Departure: dep,
			Reason: "non-control: normal dwell",
		}
		return tr.events[stop]
	}

	prev := s.prevTripAt(tr, stop)
	if prev == nil {
		dep := atSec + sc.Stops[stop].DwellSec
		tr.events[stop] = StopEvent{
			reported: true, Arrival: atSec, Departure: dep,
			Reason: "control: no preceding trip, record only",
		}
		return tr.events[stop]
	}

	prevArr := prev.events[stop].Arrival
	gap := atSec - prevArr
	switch {
	case gap > 2*sc.HeadwaySec:
		s.handleBigGap(tr, prev, stop, atSec, gap)
	case gap*2 < sc.HeadwaySec:
		s.handleBunch(tr, prev, stop, atSec, gap)
	default:
		dep := atSec + sc.Stops[stop].DwellSec
		tr.events[stop] = StopEvent{
			reported: true, Arrival: atSec, Departure: dep,
			Reason: fmt.Sprintf("control: gap=%d within [H/2,2H], record only", gap),
		}
	}
	return tr.events[stop]
}

// normalDeparture 写入无干预的到站记录。
func (s *Service) recordOnly(tr *trip, stop int, atSec int64, bunched bool, reason string) {
	dep := atSec + s.scheme.Stops[stop].DwellSec
	tr.events[stop] = StopEvent{
		reported: true, Arrival: atSec, Departure: dep,
		Intervention: InterventionNone, bunched: bunched, Reason: reason,
	}
}
