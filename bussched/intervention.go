package bussched

import "fmt"

// tryInsert 在控制站到达间隔严格大于两倍目标间隔时尝试备车插入。
// 取等不算；备车池为空时只记录大间隔事件。
func (s *Service) tryInsert(tr *tripState, idx int, t int64) {
	l := s.line
	name := l.stationName(idx)
	pred, prec := s.predAt(tr.no, idx)
	if pred == nil {
		return
	}
	gap := t - prec.arr
	if gap <= 2*l.plan.Headway {
		return
	}
	if len(s.reserves) == 0 {
		s.log(t, name, tr.no, "大间隔",
			fmt.Sprintf("与前车 %s 到站间隔 %d 秒超过两倍目标间隔，备车池为空，仅记录", pred.no.String(), gap))
		return
	}
	rno := s.reserves[0] // 取车次号最小的备车
	s.reserves = s.reserves[1:]
	driver := s.resDrv[rno]
	delete(s.resDrv, rno)
	newNo := Mid(pred.no, tr.no)
	arr := prec.arr + l.plan.Headway // 本应到达的时刻
	nt := &tripState{
		no:        newNo,
		driver:    driver,
		start:     arr - l.planArr[idx], // 虚拟首站时刻，使计划到站对齐插入点
		startIdx:  idx,
		dutyStart: arr,
		next:      idx + 1,
		records:   make(map[int]*stationRec),
		bunched:   make(map[int]bool),
		alight:    make(map[int]bool),
	}
	dep := arr + l.plan.Dwell[idx]
	if dep < prec.dep {
		dep = prec.dep
	}
	nt.records[idx] = &stationRec{arr: arr, dep: dep, kind: KindInsert,
		reason: fmt.Sprintf("备车 %s 插入于 %s 与 %s 之间", rno.String(), pred.no.String(), tr.no.String())}
	s.trips[newNo] = nt
	s.order = insertSorted(s.order, newNo)
	s.log(t, name, newNo, "备车插入",
		fmt.Sprintf("间隔 %d 秒超过两倍目标间隔，备车 %s 以车次 %s 于 %d 插入运行", gap, rno.String(), newNo.String(), arr))
}

// ApplyIntervention 对被判串车的车辆施加干预（KindHold 或 KindSkip）。
// 未判串车的车辆报错 ErrNotBunched。
func (s *Service) ApplyIntervention(no TripNo, station string, kind Kind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind != KindHold && kind != KindSkip {
		return newErr(ErrInvalidParam, "干预类别只能是扣车或跳站")
	}
	if station == "" {
		return newErr(ErrInvalidParam, "站点名不能为空")
	}
	tr, ok := s.trips[no]
	if !ok {
		return newErr(ErrTripNotFound, "车次 "+no.String()+" 不存在")
	}
	idx, ok := s.line.index[station]
	if !ok {
		return newErr(ErrStationNotFound, "站点 "+station+" 不存在")
	}
	if !tr.bunched[idx] {
		return newErr(ErrNotBunched, fmt.Sprintf("车次 %s 在 %s 未判串车", no.String(), station))
	}
	if kind == KindSkip {
		s.applySkip(tr, idx)
		return nil
	}
	s.applyHold(tr, idx)
	return nil
}

// applyHold 扣车：使本车与前车在本站的离站间隔等于目标间隔，
// 单次不超过扣车上限；超过最晚允许离站时刻则改判跳站；
// 导致司机工时超限则降级为不干预（不改判跳站）。
func (s *Service) applyHold(tr *tripState, idx int) {
	l := s.line
	name := l.stationName(idx)
	rec := tr.records[idx]
	_, prec := s.predAt(tr.no, idx)
	desired := prec.dep + l.plan.Headway
	need := desired - rec.dep
	t := rec.arr
	if need <= 0 {
		rec.kind = KindNone
		rec.reason = "无需扣车：与前车离站间隔已不小于目标间隔"
		s.log(t, name, tr.no, "扣车", rec.reason)
		return
	}
	hold := need
	if hold > l.plan.HoldCap {
		hold = l.plan.HoldCap // 超过上限时只扣到上限
	}
	newDep := rec.dep + hold
	if _, srec := s.succAt(tr.no, idx); srec != nil && newDep > srec.dep {
		newDep = srec.dep // 不改变已发车车辆的相对次序
		hold = newDep - rec.dep
	}
	if hold <= 0 {
		rec.kind = KindNone
		rec.reason = "受后车已记录离站时刻约束，无法扣车"
		s.log(t, name, tr.no, "扣车", rec.reason)
		return
	}
	plannedArr := tr.start + tr.shift + l.planArr[idx]
	if newDep > plannedArr+l.plan.Tolerance {
		s.log(t, name, tr.no, "扣车转跳站",
			fmt.Sprintf("推后离站 %d 晚于最晚允许离站 %d", newDep, plannedArr+l.plan.Tolerance))
		s.applySkip(tr, idx)
		return
	}
	if newDep-tr.dutyStart > l.plan.MaxOnDuty {
		rec.kind = KindNone
		rec.reason = fmt.Sprintf("扣车将使司机 %s 连续在岗 %d 秒超过上限 %d 秒，降级为不干预",
			tr.driver, newDep-tr.dutyStart, l.plan.MaxOnDuty)
		s.log(t, name, tr.no, "工时降级", rec.reason)
		return
	}
	tr.shift += newDep - rec.dep
	rec.dep = newDep
	rec.kind = KindHold
	rec.reason = fmt.Sprintf("扣车 %d 秒，离站顺延至 %d", hold, newDep)
	s.log(t, name, tr.no, "扣车", rec.reason)
}

// applySkip 跳站：从本控制站连续跳过非控制站直到下一个控制站。
// 被跳过站存在下车请求则整体拒绝，退回为只记录到站。
func (s *Service) applySkip(tr *tripState, idx int) {
	l := s.line
	name := l.stationName(idx)
	rec := tr.records[idx]
	t := rec.arr
	if tr.skipUsed {
		rec.kind = KindNone
		rec.reason = "跳站被拒绝：本车次已执行过一次跳站"
		s.log(t, name, tr.no, "跳站", rec.reason)
		return
	}
	from := idx + 1
	to := l.nextControl(idx) - 1
	if from > to {
		rec.kind = KindNone
		rec.reason = "跳站被拒绝：与下一控制站之间没有可跳过的非控制站"
		s.log(t, name, tr.no, "跳站", rec.reason)
		return
	}
	for k := from; k <= to; k++ {
		if tr.alight[k] {
			rec.kind = KindNone
			rec.reason = fmt.Sprintf("跳站被拒绝：站点 %s 存在下车请求，退回为只记录到站", l.stationName(k))
			s.log(t, name, tr.no, "跳站", rec.reason)
			return
		}
	}
	tr.skipUsed = true
	tr.skipFrom, tr.skipTo = from, to
	rec.kind = KindSkip
	rec.reason = fmt.Sprintf("跳过 %s 至 %s，停站时长归零、行驶时长不变",
		l.stationName(from), l.stationName(to))
	s.log(t, name, tr.no, "跳站", rec.reason)
}
