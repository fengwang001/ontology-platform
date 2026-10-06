package bus

// evaluate 在车辆到达控制站的那一刻进行串车 / 大间隔判定。
// 判定结论立刻固化在本站记录上；扣车与跳站由随后的 Intervene 显式施加，
// 大间隔备车插入按规则自动执行。
func (s *Service) evaluate(tr *trip, station int, r *record) {
	pred := s.prevAtStation(tr, station)
	if pred == nil {
		return
	}
	pr := s.rec(pred.id, station)
	gap := r.arrival - pr.arrival
	r.predID = pred.id

	switch {
	case gap*2 < s.cfg.TargetHeadway: // 严格小于一半（整数下无四舍五入歧义）
		r.bunched = true
	case gap > 2*s.cfg.TargetHeadway: // 严格大于两倍
		r.gap = true
		s.tryInsert(tr, pred, station, r, pr.arrival)
	}
}

// tryInsert 处理大间隔：备车池非空则插入车号最小的备车，否则只记录事件原因。
func (s *Service) tryInsert(tr *trip, pred *trip, station int, r *record, predArrival int64) {
	if len(s.pool) == 0 {
		r.action = ActionNone
		r.reason = ReasonPoolEmpty
		return
	}
	id := s.pool[0]
	s.pool = s.pool[1:]
	driver := s.poolDriver[id]
	delete(s.poolDriver, id)

	tPos := s.orderPos(tr)
	key := mediant(pred.key, tr.key)

	n := s.cfg.StationCount
	base := make([]int64, n)
	for i := range base {
		base[i] = -1
	}
	insertArrival := predArrival + s.cfg.TargetHeadway
	if insertArrival < s.clock {
		insertArrival = s.clock
	}
	acc := insertArrival
	for st := station; st < n; st++ {
		base[st] = acc
		if st < n-1 {
			acc += s.cfg.Dwells[st] + s.cfg.TravelTimes[st]
		}
	}

	ntr := &trip{
		id:         id,
		driver:     driver,
		key:        key,
		base:       base,
		holds:      map[int]int64{},
		insertedAt: station,
		skipFrom:   -1,
		skipTo:     -1,
		dutyStart:  insertArrival,
		lastReport: station,
		lastFinal:  station - 1,
	}
	s.trips[id] = ntr
	s.orderKeys = append(s.orderKeys, frac{})
	s.orderTrips = append(s.orderTrips, nil)
	copy(s.orderKeys[tPos+1:], s.orderKeys[tPos:])
	copy(s.orderTrips[tPos+1:], s.orderTrips[tPos:])
	s.orderKeys[tPos] = key
	s.orderTrips[tPos] = ntr

	nr := s.newTripRecord(ntr, station, insertArrival, true)
	nr.action = ActionInsert
	nr.departure = insertArrival + s.cfg.Dwells[station]
	nr.finalized = true

	r.action = ActionInsert
	r.reason = ReasonNone
	r.insTrip = id
}

// Intervene 对已判串车的车次在指定控制站施加干预。
// kind 为 ActionHold（扣车）或 ActionSkip（跳站）。
// 工时超限不是错误：返回 info 且 Action=none、Reason=duty-limit。
func (s *Service) Intervene(tripID int64, station int, kind Action) (*StopInfo, error) {
	if tripID <= 0 || station < 0 {
		return nil, ErrInvalidArgument
	}
	if kind != ActionHold && kind != ActionSkip {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tr, ok := s.trips[tripID]
	if !ok {
		return nil, ErrTripNotFound
	}
	if station >= s.cfg.StationCount {
		return nil, ErrStationNotFound
	}
	r := s.rec(tripID, station)
	if r == nil || !r.reported || !s.control[station] {
		return nil, ErrNotBunched
	}
	if station < tr.lastReport {
		return nil, ErrOutOfOrder
	}
	if r.finalized {
		return nil, ErrOutOfOrder
	}
	if !r.bunched {
		return nil, ErrNotBunched
	}

	if kind == ActionHold {
		s.applyHold(tr, station, r, true)
	} else {
		s.applySkip(tr, station, r)
	}
	return s.infoLocked(tr, station, r), nil
}

// applyHold 执行扣车判定链：先工时，再最晚离站（不通过则改判跳站），
// 最后施加 min(需求, 扣车上限) 的推后。
func (s *Service) applyHold(tr *trip, station int, r *record, allowFallbackSkip bool) {
	plannedArrival := s.PlanArrival(tr, station)
	plannedDeparture := s.planDeparture(tr, station, plannedArrival)
	pred := s.prevAtStation(tr, station)
	target := plannedDeparture
	if pred != nil {
		pArr := s.rec(pred.id, station).arrival
		target = s.effectiveDeparture(pred, station, pArr) + s.cfg.TargetHeadway
	}
	want := target - plannedDeparture
	if want <= 0 {
		r.action = ActionNone
		r.reason = ReasonNone
		r.holdSec = 0
		return
	}

	delay := want
	reason := ReasonNone
	if delay >= s.cfg.HoldCap {
		delay = s.cfg.HoldCap
		reason = ReasonHoldCap
	}

	// 工时：扣车等待计入在岗；推后后连续在岗超上限则拒绝扣车，且不改判跳站。
	if plannedDeparture+delay-tr.dutyStart > s.cfg.MaxDutySeconds {
		r.action = ActionNone
		r.reason = ReasonDutyLimit
		r.holdSec = 0
		return
	}

	departure := plannedDeparture + delay
	latest := plannedArrival + s.cfg.DepartureTolerance
	if departure > latest {
		if allowFallbackSkip {
			// 超过最晚允许离站：改为尝试跳站（跳站不通过则只记录到站）。
			s.applySkip(tr, station, r)
		}
		return
	}

	r.action = ActionHold
	r.reason = reason
	r.holdSec = delay
	r.departure = departure
	r.finalized = true
	tr.lastFinal = station
	if tr.holds == nil {
		tr.holds = map[int]int64{}
	}
	tr.holds[station] += delay
}

// applySkip 执行跳站判定链：连续跳过到下一个控制站之间的非控制站。
func (s *Service) applySkip(tr *trip, station int, r *record) {
	switch {
	case tr.skipUsed:
		r.action = ActionNone
		r.reason = ReasonSkipUsed
		return
	}
	next := s.nextControl(station)
	if next < 0 {
		r.action = ActionNone
		r.reason = ReasonNoNextControl
		return
	}

	to := next
	for st := station + 1; st < to; st++ {
		if _, ok := s.reqs[[2]int64{tr.id, int64(st)}]; ok {
			r.action = ActionNone
			r.reason = ReasonAlightRequest
			return
		}
	}

	tr.skipUsed = true
	tr.skipFrom = station
	tr.skipTo = to

	plannedArrival := s.PlanArrival(tr, station)
	r.action = ActionSkip
	r.reason = ReasonNone
	r.departure = s.planDeparture(tr, station, plannedArrival)
	r.finalized = true
	tr.lastFinal = station

	// 预生成被跳过站记录（停站归零），保证查询与后续上报顺序判定一致。
	for st := station + 1; st < to; st++ {
		s.finalizeAutoSkip(tr, st)
	}
}

// infoLocked 构造查询视图（调用方持锁）。
func (s *Service) infoLocked(tr *trip, station int, r *record) *StopInfo {
	info := &StopInfo{
		TripID:   tr.id,
		Station:  station,
		Arrival:  r.arrival,
		Reported: r.reported,
		Skipped:  r.skipped,
		Action:   r.action,
		Reason:   r.reason,
		HoldSec:  r.holdSec,
		Inserted: r.insTrip,
		PrevTrip: r.predID,
	}
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
		default:
			info.Verdict = VerdictNormal
		}
	}
	return info
}
