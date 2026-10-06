package bus

import "fmt"

// handleBigGap 处理到达间隔严格大于 2H 的情形：备车池非空则插入最小号备车，
// 否则只记录大间隔事件（不报错）。
func (s *Service) handleBigGap(tr, prev *trip, stop int, atSec, gap int64) {
	sc := s.scheme
	targetArr := prev.events[stop].Arrival + sc.HeadwaySec
	if len(s.spares) == 0 {
		s.recordOnly(tr, stop, atSec, false,
			fmt.Sprintf("control: big-gap gap=%d > 2H=%d, spare pool empty, event only", gap, 2*sc.HeadwaySec))
		return
	}
	sp := s.spares[0]
	s.spares = s.spares[1:]
	newID := s.chooseInsertID(prev.id, tr.id, sp.id)
	s.insertSpareLocked(sp, newID, tr, stop, targetArr)

	dep := atSec + sc.Stops[stop].DwellSec
	tr.events[stop] = StopEvent{
		reported: true, Arrival: atSec, Departure: dep,
		Intervention: InterventionNone,
		Reason:       fmt.Sprintf("control: big-gap gap=%d > 2H, spare trip %d inserted at t=%d", gap, newID, targetArr),
	}
}

// chooseInsertID 为被选中的备车选择介于前后两车之间的车次号：
// 备车自带号在区间内则优先复用，否则取区间内最小未占用号。
func (s *Service) chooseInsertID(lo, hi, spareID int64) int64 {
	if spareID > lo && spareID < hi {
		if _, used := s.trips[spareID]; !used {
			return spareID
		}
	}
	for id := lo + 1; id < hi; id++ {
		if _, used := s.trips[id]; used {
			continue
		}
		inPool := false
		for _, sp := range s.spares {
			if sp.id == id && sp.id != spareID {
				inPool = true
			}
		}
		if !inPool {
			return id
		}
	}
	mid := lo + (hi-lo)/2
	if mid <= lo {
		mid = lo + 1
	}
	return mid
}

// insertSpareLocked 构造插入车次并落库：它从 stop 站进入运行，
// 本站到站时刻即“本应到达”时刻；此前各站保持未上报。
func (s *Service) insertSpareLocked(sp spareInfo, newID int64, behind *trip, stop int, atSec int64) {
	tr := newTrip(newID, sp.driver, true, len(s.scheme.Stops))
	tr.entryStop = stop
	tr.nextStop = stop + 1
	tr.dutyStart = atSec
	tr.anchor = atSec
	dep := atSec + s.scheme.Stops[stop].DwellSec
	tr.events[stop] = StopEvent{
		reported: true, Arrival: atSec, Departure: dep,
		Intervention: InterventionSpareInsert,
		Reason:       fmt.Sprintf("spare pool id=%d inserted at stop %d, scheduled t=prevArr+H", sp.id, stop),
	}
	s.trips[newID] = tr
	idx := s.tripIndex(behind)
	s.order = append(s.order, 0)
	copy(s.order[idx+1:], s.order[idx:])
	s.order[idx] = newID
	s.logf("SPARE_INSERT sparePoolID=%d newTripID=%d stop=%d t=%d dep=%d order=%v",
		sp.id, newID, stop, atSec, dep, s.order)
}

// handleBunch 在控制站对判串车车辆依次尝试扣车、跳站，都不满足则只记录。
func (s *Service) handleBunch(tr, prev *trip, stop int, atSec, gap int64) {
	sc := s.scheme
	prevDep := prev.events[stop].Departure
	want := prevDep + sc.HeadwaySec
	hold := want - (atSec + sc.Stops[stop].DwellSec)
	if hold < 0 {
		hold = 0
	}
	capped := hold
	if capped > sc.HoldCapSec {
		capped = sc.HoldCapSec
	}
	plannedArr := sc.planArrival(tr.anchor, stop) + tr.delay
	latestDep := plannedArr + sc.ToleranceSec

	if hold > sc.HoldCapSec {
		// 单次扣车超过上限：先看“扣到上限”是否越过最晚允许离站。
		if atSec+sc.Stops[stop].DwellSec+capped > latestDep {
			s.tryOrRecord(tr, stop, atSec, gap,
				fmt.Sprintf("bunched gap=%d < H/2=%d; hold needs %d > cap %d and capped dep t=%d > latest %d",
					gap, (sc.HeadwaySec+1)/2, hold, sc.HoldCapSec,
					atSec+sc.Stops[stop].DwellSec+capped, latestDep))
			return
		}
		dep := atSec + sc.Stops[stop].DwellSec + capped
		if s.dutyExceeded(tr, dep) {
			s.recordOnly(tr, stop, atSec, true,
				fmt.Sprintf("bunched; capped hold %d rejected: duty would exceed cap %d", capped, sc.DutyCapSec))
			return
		}
		s.applyHold(tr, stop, atSec, dep, capped,
			fmt.Sprintf("bunched gap=%d < H/2; hold %d capped to %d, dep gap to prev=%d (< H due to cap)",
				gap, hold, capped, dep-prevDep))
		return
	}

	// hold <= cap：先做最晚离站判定，超过则改判跳站。
	dep := atSec + sc.Stops[stop].DwellSec + hold
	if dep > latestDep {
		s.tryOrRecord(tr, stop, atSec, gap,
			fmt.Sprintf("bunched gap=%d < H/2; hold dep t=%d > latest allowed %d (plannedArr=%d +tol=%d), reclassify to skip",
				gap, dep, latestDep, plannedArr, sc.ToleranceSec))
		return
	}
	if s.dutyExceeded(tr, dep) {
		s.recordOnly(tr, stop, atSec, true,
			fmt.Sprintf("bunched; hold %d rejected: on-duty span would exceed cap %d, downgrade to no intervention",
				hold, sc.DutyCapSec))
		return
	}
	s.applyHold(tr, stop, atSec, dep, hold,
		fmt.Sprintf("bunched gap=%d < H/2; hold %d makes departure gap exactly H=%d", gap, hold, sc.HeadwaySec))
}

func (s *Service) dutyExceeded(tr *trip, atEnd int64) bool {
	return atEnd-tr.dutyStart > s.scheme.DutyCapSec
}

func (s *Service) applyHold(tr *trip, stop int, atSec, dep, hold int64, reason string) {
	tr.events[stop] = StopEvent{
		reported: true, Arrival: atSec, Departure: dep,
		Intervention: InterventionHold, bunched: true, Reason: reason,
	}
	tr.delay += hold
}

// tryOrRecord 先尝试跳站，跳站不可行时只记录到站。
func (s *Service) tryOrRecord(tr *trip, stop int, atSec, gap int64, why string) {
	if ok, reason := s.applySkip(tr, stop, atSec, why); ok {
		return
	} else {
		s.recordOnly(tr, stop, atSec, true, reason)
	}
}

// applySkip 从当前控制站连续跳过非控制站直到下一控制站；
// 不跨控制站、每车次最多一次；被跳过站存在下车请求则整次跳站拒绝（不改状态）。
func (s *Service) applySkip(tr *trip, stop int, atSec int64, why string) (bool, string) {
	sc := s.scheme
	if tr.skipUsed {
		return false, why + "; skip refused: skip already used by this trip"
	}
	next := sc.NextControl(stop)
	if next < 0 {
		return false, why + "; skip refused: no later control stop"
	}
	for k := stop + 1; k < next; k++ {
		if tr.alighting[k] {
			return false, fmt.Sprintf("%s; skip refused: alighting request registered at stop %d", why, k)
		}
	}
	tr.skipUsed = true
	dep := atSec
	tr.events[stop] = StopEvent{
		reported: true, Arrival: atSec, Departure: dep,
		Intervention: InterventionSkip, bunched: true,
		Reason: why + fmt.Sprintf("; skip stops %d..%d until control %d, dwell zeroed", stop+1, next-1, next),
	}
	t := dep
	for k := stop + 1; k < next; k++ {
		t += sc.Travel[k-1]
		tr.events[k] = StopEvent{
			reported: true, Arrival: t, Departure: t,
			Intervention: InterventionSkip, bunched: true,
			Reason: fmt.Sprintf("skipped as fast train between controls %d and %d, dwell=0", stop, next),
		}
		tr.nextStop = k + 1
	}
	return true, tr.events[stop].Reason
}
