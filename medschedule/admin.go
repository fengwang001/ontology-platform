package medschedule

import "sort"

// livePoints 生成医嘱在 [lo,hi] 内“当前有效”（未被重排截断）的计划点。
// 每个点返回时保证它属于某段且未被后续段截断；点的处理状态由调用方判定。
// 该函数只按区间与段做二分/常数段扫描，与历史点总数无关。

// segPointLive 判断 seg 内点 t 是否有效：截断段中 t 必须 < 下一段起点。
func segPointLive(segs []intervalSeg, segIdx, t int64) bool {
	seg := segs[segIdx]
	if seg.cut < 0 {
		return true
	}
	return t < seg.cut
}

// lastSegActiveStart 返回最后一段起点。
func lastSegStart(segs []intervalSeg) int64 { return segs[len(segs)-1].start }

// findIntervalOnTime 在固定间隔医嘱中寻找窗口含 now 且未处理的点。
func (s *System) findIntervalOnTime(o *order, now int64) (int64, bool) {
	segs := o.segs
	seg := segs[len(segs)-1]
	var found int64
	hit := false
	// 窗口下界可能早于段起点（补给时刻 now_m 可落在下一点窗口内，
	// 此时段起点 now_m+H 仍可能满足 t-W <= now_m），因此用 start 本身裁剪。
	lb := now - s.w
	if seg.start > lb {
		lb = seg.start
	}
	intervalCandidates(seg.start, o.h, lb, now+s.w, func(t int64) {
		if hit {
			return
		}
		if _, done := o.records[t]; done {
			return
		}
		found, hit = t, true
	})
	return found, hit
}

// findIntervalMakeupTarget 寻找固定间隔医嘱当前可补给的唯一漏给点。
func (s *System) findIntervalMakeupTarget(o *order, now int64) (int64, bool) {
	seg := o.segs[len(o.segs)-1]
	// 计算满足 p+W < now 的最大 k：start+k*h <= now-W-1。
	upper := now - s.w - 1
	if upper < seg.start {
		return 0, false
	}
	km := (upper - seg.start) / o.h
	t := seg.start + km*o.h
	if _, done := o.records[t]; done {
		return 0, false
	}
	// 可补给要求下一点窗口尚未开始：now < t+H-W（恰等于窗口起始即拒绝）。
	if now >= t+o.h-s.w {
		return 0, false
	}
	return t, true
}

// nextIntervalPoint 返回最后一段中严格晚于 t 的最小计划点（是否存在）。
func nextIntervalPoint(o *order, t int64) (int64, bool) {
	seg := o.segs[len(o.segs)-1]
	k := firstIndexAtOrAfter(seg.start, o.h, t+1)
	return seg.start + k*o.h, true
}

// findDailyOnTime 寻找固定时点医嘱窗口含 now 且未处理的点。
func (s *System) findDailyOnTime(o *order, now int64) (int64, bool) {
	var found int64
	hit := false
	// 窗口最多跨两天，逐点常数扫描。
	dailyCandidates(o.timesOfDay, o.createdAt, now-s.w, now+s.w, func(t int64) {
		if hit {
			return
		}
		if _, done := o.records[t]; done {
			return
		}
		found, hit = t, true
	})
	return found, hit
}

// findDailyMakeupTarget 寻找固定时点医嘱当前可补给的唯一漏给点。
func (s *System) findDailyMakeupTarget(o *order, now int64) (int64, bool) {
	p, ok := dailyPointAtOrBefore(o.timesOfDay, o.createdAt, now-s.w-1)
	if !ok {
		return 0, false
	}
	if _, done := o.records[p]; done {
		return 0, false
	}
	nxt, has := dailyPointAtOrAfter(o.timesOfDay, o.createdAt, p+1)
	if has && now >= nxt-s.w {
		return 0, false // 下一点窗口已开始，冻结为漏给
	}
	return p, true
}

// checkSafetyGap 校验同一患者同一药品（跨医嘱）的最小安全给药间隔。
func (s *System) checkSafetyGap(patient, drug string, now int64) error {
	d, ok := s.drugs[drug]
	if !ok {
		return errNotFound("drug not registered: " + drug)
	}
	if last, ok := s.lastAdmin[patientDrugKey(patient, drug)]; ok {
		if now-last < d.MinIntervalSec {
			return errInterval("patient drug safety interval violated")
		}
	}
	return nil
}

func (s *System) commitAdmin(o *order, p int64, st Status, now int64) {
	o.records[p] = st
	if st == StatusGivenOnTime || st == StatusMadeUp {
		s.lastAdmin[patientDrugKey(o.patient, o.drug)] = now
	}
	s.commitClock(now)
}

// Administer 登记按时给药（now 即实际给药时刻）。
func (s *System) Administer(now int64, orderID string) error {
	if !validNow(now) || !validateIDs(orderID) {
		return errInvalid("invalid administer parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return errNotFound("order not found: " + orderID)
	}
	if !o.active() {
		return errBadState("order stopped: " + orderID)
	}
	if o.kind == freqPRN {
		return errBadState("PRN order requires AdministerPRN: " + orderID)
	}
	var p int64
	var found bool
	if o.kind == freqInterval {
		p, found = s.findIntervalOnTime(o, now)
	} else {
		p, found = s.findDailyOnTime(o, now)
	}
	if !found {
		return errNoPoint("no unhandled scheduled point whose on-time window contains now")
	}
	if err := s.checkSafetyGap(o.patient, o.drug, now); err != nil {
		return err
	}
	s.commitAdmin(o, p, StatusGivenOnTime, now)
	return nil
}

// Refuse 登记患者拒服。
func (s *System) Refuse(now int64, orderID string) error {
	if !validNow(now) || !validateIDs(orderID) {
		return errInvalid("invalid refuse parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return errNotFound("order not found: " + orderID)
	}
	if !o.active() {
		return errBadState("order stopped: " + orderID)
	}
	if o.kind == freqPRN {
		return errBadState("PRN order has no scheduled points: " + orderID)
	}
	var p int64
	var found bool
	if o.kind == freqInterval {
		p, found = s.findIntervalOnTime(o, now)
	} else {
		p, found = s.findDailyOnTime(o, now)
	}
	if !found {
		return errNoPoint("no unhandled scheduled point whose on-time window contains now")
	}
	// 拒服不计实际给药，不参与间隔判定。
	o.records[p] = StatusRefused
	s.commitClock(now)
	return nil
}

// MakeUp 登记漏给点补给。
func (s *System) MakeUp(now int64, orderID string) error {
	if !validNow(now) || !validateIDs(orderID) {
		return errInvalid("invalid makeup parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return errNotFound("order not found: " + orderID)
	}
	if !o.active() {
		return errBadState("order stopped: " + orderID)
	}
	if o.kind == freqPRN {
		return errBadState("PRN order has no scheduled points: " + orderID)
	}
	var p, nxt int64
	var found, hasNext bool
	if o.kind == freqInterval {
		p, found = s.findIntervalMakeupTarget(o, now)
		if found {
			nxt, hasNext = nextIntervalPoint(o, p)
		}
	} else {
		p, found = s.findDailyMakeupTarget(o, now)
		if found {
			nxt, hasNext = dailyPointAtOrAfter(o.timesOfDay, o.createdAt, p+1)
		}
	}
	if !found {
		return errNoPoint("no makeable missed point")
	}
	if err := s.checkSafetyGap(o.patient, o.drug, now); err != nil {
		return err
	}
	// 补给时刻必须严格早于下一点窗口起始；无下一点时不施加该限制。
	if hasNext && now >= nxt-s.w {
		return errMakeup("now must be strictly before next point window start")
	}
	if o.kind == freqInterval {
		// 截断当前最后一段，另起新段，起点为 now+H。
		old := &o.segs[len(o.segs)-1]
		newStart := now + o.h
		old.cut = newStart
		old.madeUpAt = p
		o.segs = append(o.segs, intervalSeg{start: newStart, cut: -1, madeUpAt: -1})
	}
	s.commitAdmin(o, p, StatusMadeUp, now)
	return nil
}

// AdministerPRN 必要时给药。
func (s *System) AdministerPRN(now int64, orderID string) error {
	if !validNow(now) || !validateIDs(orderID) {
		return errInvalid("invalid prn parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return errNotFound("order not found: " + orderID)
	}
	if !o.active() {
		return errBadState("order stopped: " + orderID)
	}
	if o.kind != freqPRN {
		return errBadState("not a PRN order: " + orderID)
	}
	// 同医嘱最小间隔（含上一次 PRN 实际给药）。
	if n := len(o.prnDoses); n > 0 {
		if now-o.prnDoses[n-1] < o.prnMinGap {
			return errInterval("PRN intra-order interval violated")
		}
	}
	// 同患者同药品最小安全间隔（跨医嘱）。
	if err := s.checkSafetyGap(o.patient, o.drug, now); err != nil {
		return err
	}
	// 滚动 24h：窗口为 (now-86400, now]，统计已实际给药次数。
	cutoff := now - dayLen
	count := 0
	for i := len(o.prnDoses) - 1; i >= 0; i-- {
		if o.prnDoses[i] <= cutoff {
			break
		}
		count++
	}
	if int64(count) >= o.prnMax24 {
		return errPRNLimit("rolling 24h PRN count limit reached")
	}
	o.prnDoses = append(o.prnDoses, now)
	sort.Slice(o.prnDoses, func(i, j int) bool { return o.prnDoses[i] < o.prnDoses[j] })
	s.lastAdmin[patientDrugKey(o.patient, o.drug)] = now
	s.commitClock(now)
	return nil
}
