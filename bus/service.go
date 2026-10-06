package bus

// PlanArrival 返回某车次在某站“当前计划到站时刻”（含此前所有扣车导致的顺延）。
// 规则：从最近一个已实际上报站出发；锚点站本身的停站/扣车不再重复计入
// （已被锚点之后的真实到站吸收），锚点之后的各站按方案停站、行驶与扣车推进。
// 该计算只依赖该车次自身数据，与线路累计上报总量无关。
func (s *Service) PlanArrival(tr *trip, station int) int64 {
	if station < tr.insertedAt {
		return -1
	}
	if r := s.rec(tr.id, station); r != nil && r.reported && !r.autoBorn {
		return r.arrival
	}
	anchor := tr.insertedAt
	anchorArrival := tr.base[anchor]
	for st := station - 1; st >= tr.insertedAt; st-- {
		if r := s.rec(tr.id, st); r != nil && r.reported && !r.autoBorn {
			anchor = st
			anchorArrival = r.arrival
			break
		}
	}
	arrival := anchorArrival
	for st := anchor; st < station; st++ {
		// 本站扣车总是顺延下一段；停站仅在非跳过且非锚点站时加入，
		// 因为锚点站的停站已被其后首个真实到站吸收。
		if h, ok := tr.holds[st]; ok {
			arrival += h
		}
		if st != anchor && !s.isSkipped(tr, st) {
			arrival += s.cfg.Dwells[st]
		}
		arrival += s.cfg.TravelTimes[st]
	}
	return arrival
}

// planDeparture 返回计划离站时刻；跳过站停站归零。
func (s *Service) planDeparture(tr *trip, station int, plannedArrival int64) int64 {
	if s.isSkipped(tr, station) {
		return plannedArrival
	}
	return plannedArrival + s.cfg.Dwells[station]
}

func (s *Service) isSkipped(tr *trip, station int) bool {
	return tr.skipUsed && station > tr.skipFrom && station < tr.skipTo
}

func (s *Service) rec(tripID int64, station int) *record {
	return s.recs[[2]int64{tripID, int64(station)}]
}

// Snapshot 导出全部已记录站点信息。仅供离线重放对照使用；
// 它是显式的全量遍历，与线上查询 Query 的 O(1) 路径完全分离。
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{
		Clock: s.clock,
		Stops: map[[2]int64]StopInfo{},
		Pool:  append([]int64(nil), s.pool...),
	}
	for _, tr := range s.orderTrips {
		snap.Trips = append(snap.Trips, tr.id)
		for st := 0; st < s.cfg.StationCount; st++ {
			r := s.rec(tr.id, st)
			if r == nil {
				continue
			}
			info := &StopInfo{
				TripID:   tr.id,
				Station:  st,
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
				info.Departure = s.planDeparture(tr, st, s.PlanArrival(tr, st))
			}
			if s.control[st] {
				switch {
				case r.bunched:
					info.Verdict = VerdictBunch
				case r.gap:
					info.Verdict = VerdictGap
				case r.reported:
					info.Verdict = VerdictNormal
				}
			}
			snap.Stops[([2]int64{tr.id, int64(st)})] = *info
		}
	}
	return snap
}

// orderPos 返回车次在运行序列中的位置；不在序列中返回 -1。
func (s *Service) orderPos(tr *trip) int {
	for i, t := range s.orderTrips {
		if t == tr {
			return i
		}
	}
	return -1
}

// prevAtStation 返回在本站已有到站记录的最近前序车次。
func (s *Service) prevAtStation(tr *trip, station int) *trip {
	p := s.orderPos(tr)
	for i := p - 1; i >= 0; i-- {
		if r := s.rec(s.orderTrips[i].id, station); r != nil && r.reported {
			return s.orderTrips[i]
		}
	}
	return nil
}

// finalizedDeparture 返回前序车次在本站的最终离站；尚未最终确定时回退为其计划离站。
func (s *Service) effectiveDeparture(tr *trip, station int, plannedArrival int64) int64 {
	r := s.rec(tr.id, station)
	if r != nil && r.finalized {
		return r.departure
	}
	if r != nil && r.reported {
		plannedArrival = r.arrival
	}
	return s.planDeparture(tr, station, plannedArrival)
}

// nextControl 返回 strictlyAfter 之后的第一个控制站；不存在返回 -1。
func (s *Service) nextControl(strictlyAfter int) int {
	for st := strictlyAfter + 1; st < s.cfg.StationCount; st++ {
		if s.control[st] {
			return st
		}
	}
	return -1
}

// newTripRecord 为某车次的某个站建立到站记录（首发 / 备车插入 / 正常上报共用）。
func (s *Service) newTripRecord(tr *trip, station int, arrival int64, auto bool) *record {
	key := [2]int64{tr.id, int64(station)}
	r := s.recs[key]
	if r == nil {
		r = &record{}
		s.recs[key] = r
	}
	r.arrival = arrival
	r.reported = true
	r.autoBorn = auto
	r.departure = s.planDeparture(tr, station, arrival)
	return r
}

// ScheduleTrip 在首发站投放一辆按计划发车的车次。
// 车次号先后即计划发车次序；at 为首发站到站（开始服务）时刻。
func (s *Service) ScheduleTrip(tripID, driverID, at int64) error {
	if tripID <= 0 || driverID <= 0 || at < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.clock {
		return ErrClockRollback
	}
	if _, ok := s.trips[tripID]; ok {
		return ErrInvalidArgument
	}
	if _, inPool := s.poolDriver[tripID]; inPool {
		return ErrInvalidArgument
	}
	if _, ok := s.drivers[driverID]; !ok {
		return ErrDriverNotFound
	}
	if tripID <= s.lastShed {
		return ErrInvalidArgument
	}

	n := s.cfg.StationCount
	base := make([]int64, n)
	for st := 1; st < n; st++ {
		base[st] = base[st-1] + s.cfg.Dwells[st-1] + s.cfg.TravelTimes[st-1]
	}
	tr := &trip{
		id:         tripID,
		driver:     driverID,
		key:        fracInt(tripID),
		base:       base,
		holds:      map[int]int64{},
		insertedAt: 0,
		skipFrom:   -1,
		skipTo:     -1,
		dutyStart:  at,
		lastReport: 0,
		lastFinal:  -1,
	}
	s.trips[tripID] = tr
	s.lastShed = tripID
	s.orderKeys = append(s.orderKeys, tr.key)
	s.orderTrips = append(s.orderTrips, tr)

	r := s.newTripRecord(tr, 0, at, false)
	r.finalized = true
	r.departure = at + s.cfg.Dwells[0]
	tr.lastFinal = 0

	s.clock = at
	return nil
}

// RegisterAlight 登记某车次在某站存在下车请求。
// 重复登记幂等；车次必须已经在运行序列中。
func (s *Service) RegisterAlight(tripID int64, station int, at int64) error {
	if tripID <= 0 || station < 0 || at < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.clock {
		return ErrClockRollback
	}
	tr, ok := s.trips[tripID]
	if !ok {
		return ErrTripNotFound
	}
	if station < 0 || station >= s.cfg.StationCount {
		return ErrStationNotFound
	}
	if station < tr.insertedAt {
		return ErrOutOfOrder
	}
	s.reqs[[2]int64{tripID, int64(station)}] = struct{}{}
	s.clock = at
	return nil
}

// ReportArrival 上报车辆到站。同一站重复上报拒绝；站序必须严格递增，
// 且不能跨过未上报的非跳过站。到达控制站时进行串车 / 大间隔判定。
func (s *Service) ReportArrival(tripID int64, station int, at int64) error {
	if tripID <= 0 || station < 0 || at < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.clock {
		return ErrClockRollback
	}
	tr, ok := s.trips[tripID]
	if !ok {
		return ErrTripNotFound
	}
	if station >= s.cfg.StationCount {
		return ErrStationNotFound
	}

	if r := s.rec(tripID, station); r != nil && r.reported {
		return ErrDuplicateReport
	}
	if station <= tr.lastReport {
		return ErrOutOfOrder
	}
	if station < tr.insertedAt {
		return ErrOutOfOrder
	}

	// 从上次上报站到本次站之间的缺口：非跳过站必须依次上报，不能跨站。
	expected := tr.lastReport + 1
	for st := expected; st < station; st++ {
		if !s.isSkipped(tr, st) {
			return ErrOutOfOrder
		}
	}

	// 最终确定此前缺口内（被跳过）各站的到离时刻。
	for st := expected; st < station; st++ {
		s.finalizeAutoSkip(tr, st)
	}

	// 若上一站离站尚未最终确定（正常流程：当前站到站即冻结上一站离站）。
	if tr.lastFinal < station-1 {
		prev := station - 1
		if pr := s.rec(tr.id, prev); pr != nil {
			pa := s.PlanArrival(tr, prev)
			pr.departure = s.planDeparture(tr, prev, pa)
			pr.finalized = true
			tr.lastFinal = prev
		}
	}

	r := s.newTripRecord(tr, station, at, false)
	tr.lastReport = station
	s.clock = at

	if s.control[station] {
		s.evaluate(tr, station, r)
	}
	return nil
}

// finalizeAutoSkip 为被跳过站自动生成到离记录并冻结（停站归零）。
func (s *Service) finalizeAutoSkip(tr *trip, station int) {
	if existing := s.rec(tr.id, station); existing != nil {
		if !existing.finalized {
			existing.departure = existing.arrival
			existing.finalized = true
			tr.lastFinal = station
		}
		return
	}
	pa := s.PlanArrival(tr, station)
	r := &record{
		arrival:   pa,
		departure: pa,
		finalized: true,
		reported:  true,
		autoBorn:  true,
		skipped:   true,
		action:    ActionSkip,
	}
	s.recs[[2]int64{tr.id, int64(station)}] = r
	tr.lastFinal = station
}

// Query 返回某车次在某站的到离时刻、判定与干预信息。
// 查询只做一次定长哈希定位与若干车次自身字段读取，复杂度 O(1)，
// 不遍历线路上的累计上报记录。
func (s *Service) Query(tripID int64, station int) (*StopInfo, error) {
	if tripID <= 0 || station < 0 {
		return nil, ErrInvalidArgument
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tr, ok := s.trips[tripID]
	if !ok {
		if _, inPool := s.poolDriver[tripID]; inPool {
			return nil, ErrTripNotFound
		}
		return nil, ErrTripNotFound
	}
	if station >= s.cfg.StationCount {
		return nil, ErrStationNotFound
	}

	info := &StopInfo{TripID: tripID, Station: station, Arrival: -1, Departure: -1, Inserted: -1}
	if station < tr.insertedAt {
		return info, nil
	}
	r := s.rec(tripID, station)
	if r == nil {
		return info, nil
	}
	info.Arrival = r.arrival
	info.Reported = r.reported
	info.Skipped = r.skipped
	info.Action = r.action
	info.Reason = r.reason
	info.HoldSec = r.holdSec
	info.Inserted = r.insTrip
	info.PrevTrip = r.predID
	if r.finalized {
		info.Departure = r.departure
		info.Finalized = true
	} else {
		info.Departure = s.planDeparture(tr, station, s.PlanArrival(tr, station))
	}
	if s.control[station] {
		switch {
		case r.bunched:
			info.Verdict = VerdictBunch
		case r.gap:
			info.Verdict = VerdictGap
		case r.reported:
			info.Verdict = VerdictNormal
		}
	}
	return info, nil
}
