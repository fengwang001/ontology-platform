package bussched

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// stationRec 为某车次在某站的实际到离记录。
type stationRec struct {
	arr    int64
	dep    int64
	kind   Kind
	reason string
}

// tripState 为单车次运行状态。
type tripState struct {
	no        TripNo
	driver    string
	start     int64 // 计划首站到站时刻（备车插入时为虚拟值）
	startIdx  int   // 首个运行车站下标
	shift     int64 // 扣车累计顺延
	dutyStart int64 // 司机上岗时刻
	next      int   // 下一个应上报的站下标
	records   map[int]*stationRec
	bunched   map[int]bool // 被判串车的控制站
	alight    map[int]bool // 已登记下车请求的站
	skipUsed  bool
	skipFrom  int // 被跳过站区间 [skipFrom, skipTo]
	skipTo    int
}

func (t *tripState) skipped(i int) bool {
	return t.skipUsed && i >= t.skipFrom && i <= t.skipTo
}

// Service 为排班与串车调整服务。所有方法可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个串行顺序。
type Service struct {
	mu       sync.Mutex
	line     *Line
	drivers  map[string]bool
	trips    map[TripNo]*tripState
	order    []TripNo // 按车次号升序
	reserves []TripNo // 备车池，升序
	resDrv   map[TripNo]string
	lastTime int64 // 上一次被接受操作的时刻
	hasTime  bool
	events   []Event
}

func NewService(line *Line) *Service {
	return &Service{
		line:    line,
		drivers: make(map[string]bool),
		trips:   make(map[TripNo]*tripState),
		resDrv:  make(map[TripNo]string),
	}
}

// RegisterDriver 注册司机。
func (s *Service) RegisterDriver(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return newErr(ErrInvalidParam, "司机编号不能为空")
	}
	if s.drivers[id] {
		return newErr(ErrInvalidParam, "司机 "+id+" 已注册")
	}
	s.drivers[id] = true
	return nil
}

// RegisterTrip 注册车次。start 为计划首站到站时刻。
// 车次号必须全局唯一；注册顺序即计划发车次序。
func (s *Service) RegisterTrip(no TripNo, driverID string, start int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start < 0 {
		return newErr(ErrInvalidParam, "计划首站到站时刻不能为负")
	}
	if _, dup := s.trips[no]; dup {
		return newErr(ErrInvalidParam, "车次 "+no.String()+" 已存在")
	}
	if !s.drivers[driverID] {
		return newErr(ErrDriverNotFound, "司机 "+driverID+" 不存在")
	}
	tr := &tripState{
		no:        no,
		driver:    driverID,
		start:     start,
		dutyStart: start,
		records:   make(map[int]*stationRec),
		bunched:   make(map[int]bool),
		alight:    make(map[int]bool),
	}
	s.trips[no] = tr
	s.order = insertSorted(s.order, no)
	return nil
}

// RegisterReserve 将车辆放入备车池。
func (s *Service) RegisterReserve(no TripNo, driverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.resDrv[no]; dup {
		return newErr(ErrInvalidParam, "备车 "+no.String()+" 已存在")
	}
	if _, dup := s.trips[no]; dup {
		return newErr(ErrInvalidParam, "车次 "+no.String()+" 已存在")
	}
	if !s.drivers[driverID] {
		return newErr(ErrDriverNotFound, "司机 "+driverID+" 不存在")
	}
	s.resDrv[no] = driverID
	s.reserves = insertSorted(s.reserves, no)
	return nil
}

// RegisterAlighting 登记某车次在某站的下车请求。
func (s *Service) RegisterAlighting(no TripNo, station string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr, ok := s.trips[no]
	if !ok {
		return newErr(ErrTripNotFound, "车次 "+no.String()+" 不存在")
	}
	idx, ok := s.line.index[station]
	if !ok {
		return newErr(ErrStationNotFound, "站点 "+station+" 不存在")
	}
	tr.alight[idx] = true
	return nil
}

func insertSorted(s []TripNo, v TripNo) []TripNo {
	i := sort.Search(len(s), func(i int) bool { return s[i] >= v })
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// predAt 返回 no 的前一车次中已在 idx 站有记录者；无则返回 nil。
func (s *Service) predAt(no TripNo, idx int) (*tripState, *stationRec) {
	i := sort.Search(len(s.order), func(i int) bool { return s.order[i] >= no })
	for j := i - 1; j >= 0; j-- {
		tr := s.trips[s.order[j]]
		if rec, ok := tr.records[idx]; ok {
			return tr, rec
		}
	}
	return nil, nil
}

// succAt 返回 no 的后一车次中已在 idx 站有记录者；无则返回 nil。
func (s *Service) succAt(no TripNo, idx int) (*tripState, *stationRec) {
	i := sort.Search(len(s.order), func(i int) bool { return s.order[i] > no })
	for ; i < len(s.order); i++ {
		tr := s.trips[s.order[i]]
		if rec, ok := tr.records[idx]; ok {
			return tr, rec
		}
	}
	return nil, nil
}

func (s *Service) log(t int64, station string, no TripNo, kind, detail string) {
	s.events = append(s.events, Event{Time: t, Station: station, Trip: no, Kind: kind, Detail: detail})
}

// ReportArrival 上报某车次到达某站。校验失败的顺序为：
// 参数非法、时钟回退、车次不存在、站点不存在、上报乱序、重复上报。
// 被拒绝的上报不改变任何状态、车次次序与时钟。
func (s *Service) ReportArrival(no TripNo, station string, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if station == "" || t < 0 {
		return newErr(ErrInvalidParam, "站点名不能为空且时刻不能为负")
	}
	if s.hasTime && t < s.lastTime {
		return newErr(ErrClockRollback, fmt.Sprintf("时刻 %d 早于上次接受时刻 %d", t, s.lastTime))
	}
	tr, ok := s.trips[no]
	if !ok {
		return newErr(ErrTripNotFound, "车次 "+no.String()+" 不存在")
	}
	idx, ok := s.line.index[station]
	if !ok {
		return newErr(ErrStationNotFound, "站点 "+station+" 不存在")
	}
	if idx > tr.next {
		return newErr(ErrOutOfOrder, fmt.Sprintf("车次 %s 应先上报第 %d 站", no.String(), tr.next))
	}
	if idx < tr.next {
		return newErr(ErrDuplicateReport, fmt.Sprintf("车次 %s 已上报过站点 %s", no.String(), station))
	}
	s.arrive(tr, idx, t)
	s.lastTime = t
	s.hasTime = true
	return nil
}

// arrive 处理一次被接受的到站上报。
func (s *Service) arrive(tr *tripState, idx int, t int64) {
	l := s.line
	name := l.stationName(idx)
	if l.control[idx] {
		s.tryInsert(tr, idx, t)
		if pred, prec := s.predAt(tr.no, idx); pred != nil {
			gap := t - prec.arr
			if 2*gap < l.plan.Headway { // 严格小于一半才算串车
				tr.bunched[idx] = true
				s.log(t, name, tr.no, "串车",
					fmt.Sprintf("与前车 %s 到站间隔 %d 秒，小于目标间隔一半 %d 秒",
						pred.no.String(), gap, l.plan.Headway/2))
			}
		}
	}
	dwell := l.plan.Dwell[idx]
	if tr.skipped(idx) {
		dwell = 0
	}
	dep := t + dwell
	if _, prec := s.predAt(tr.no, idx); prec != nil && dep < prec.dep {
		dep = prec.dep // 保证相邻车次同站离站时刻差不为负
	}
	tr.records[idx] = &stationRec{arr: t, dep: dep, kind: KindNone}
	tr.next = idx + 1
}

// QueryRecord 查询某车次在某站的到离时刻与干预类别。
// 已上报站直接查表，未到达站由前缀和 O(1) 外推，开销与累计上报数量无关。
func (s *Service) QueryRecord(no TripNo, station string) (RecordView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr, ok := s.trips[no]
	if !ok {
		return RecordView{}, newErr(ErrTripNotFound, "车次 "+no.String()+" 不存在")
	}
	idx, ok := s.line.index[station]
	if !ok {
		return RecordView{}, newErr(ErrStationNotFound, "站点 "+station+" 不存在")
	}
	if rec, ok := tr.records[idx]; ok {
		return RecordView{Arrival: rec.arr, Departure: rec.dep,
			Intervention: rec.kind, Reason: rec.reason, Actual: true}, nil
	}
	if idx < tr.startIdx {
		return RecordView{Arrival: -1, Departure: -1, Reason: "该车次不经过该站"}, nil
	}
	l := s.line
	var saved int64
	if tr.skipUsed && idx > tr.skipTo {
		saved = l.dwellPrefix[tr.skipTo+1] - l.dwellPrefix[tr.skipFrom]
	}
	arr := tr.start + tr.shift + l.planArr[idx] - saved
	dwell := l.plan.Dwell[idx]
	if tr.skipped(idx) {
		dwell = 0
	}
	return RecordView{Arrival: arr, Departure: arr + dwell, Reason: "计划外推"}, nil
}

// QueryEvents 返回全部判定与降级事件日志。
func (s *Service) QueryEvents() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// Dump 导出全部记录与事件，用于确定性重放比对。
func (s *Service) Dump() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, no := range s.order {
		tr := s.trips[no]
		fmt.Fprintf(&b, "trip %s driver=%s start=%d shift=%d next=%d skipUsed=%v\n",
			no.String(), tr.driver, tr.start, tr.shift, tr.next, tr.skipUsed)
		for i := 0; i < s.line.numStations(); i++ {
			if rec, ok := tr.records[i]; ok {
				fmt.Fprintf(&b, "  %s arr=%d dep=%d kind=%s reason=%q\n",
					s.line.stationName(i), rec.arr, rec.dep, rec.kind, rec.reason)
			}
		}
	}
	for _, e := range s.events {
		fmt.Fprintf(&b, "event t=%d %s trip=%s %s %q\n", e.Time, e.Station, e.Trip.String(), e.Kind, e.Detail)
	}
	return b.String()
}
