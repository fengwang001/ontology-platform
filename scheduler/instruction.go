package scheduler

import "sort"

// Issue 发布限电指令：区域、等级（同时须限电的组数）、左闭右开且对齐
// 时段边界的窗口。窗口起点不得早于当前时刻所在时段的下一时段起点。
func (s *Scheduler) Issue(id, regionID string, level int, start, end int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.instrs[id]; dup {
		return paramErr("指令ID重复: " + id)
	}
	r, ok := s.regions[regionID]
	if !ok {
		return paramErr("区域不存在: " + regionID)
	}
	if level < 1 || level > len(r.groups) {
		return paramErr("等级越界：须为 1..区域内组数")
	}
	if start%s.slotLen != 0 || end%s.slotLen != 0 {
		return paramErr("窗口未对齐时段边界")
	}
	if end <= start {
		return paramErr("窗口为空")
	}
	if start < s.nextSlotStart() {
		return paramErr("不允许对已开始或已结束的时段下指令")
	}
	in := &instruction{
		id:      id,
		region:  regionID,
		start:   start,
		end:     end,
		changes: []levelChange{{eff: start, level: level}},
	}
	s.instrs[id] = in
	s.events[start] = append(s.events[start], instrEvent{id: id, kind: evActivate})
	s.events[end] = append(s.events[end], instrEvent{id: id, kind: evDeactivate})
	s.endH.push(end, id, false)
	s.recomputeForecast()
	return nil
}

// Modify 改级（不得改窗口；延长或缩短窗口应取消旧指令并新建）。
// 操作时刻落在某时段内部则自下一时段起生效；恰在边界上则对当前时段生效。
func (s *Scheduler) Modify(id string, newLevel int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newLevel < 1 {
		return paramErr("等级越界：须为正整数")
	}
	in, ok := s.instrs[id]
	if !ok || in.cancelled {
		return instrErr(id)
	}
	if newLevel > len(s.regions[in.region].groups) {
		return paramErr("等级越界：须不超过区域内组数")
	}
	eff := s.effBoundary()
	in.changes = append(in.changes, levelChange{eff: eff, level: newLevel})
	if eff == s.now {
		// 恰在时段边界：立即作用于当前时段并重选。
		if _, ok := s.live[id]; ok {
			s.liveH.set(id, newLevel)
		}
		s.resettleCurrent()
	} else if in.start < eff && eff < in.end {
		s.events[eff] = append(s.events[eff], instrEvent{id: id, kind: evSetLevel})
	}
	s.recomputeForecast()
	return nil
}

// Cancel 取消指令。生效边界同改级；窗口尚未开始的指令取消后视同从未存在。
func (s *Scheduler) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.instrs[id]
	if !ok || in.cancelled {
		return instrErr(id)
	}
	eff := s.effBoundary()
	in.cancelled = true
	in.cancelEff = eff
	s.endH.push(eff, id, true)
	if eff == s.now {
		// 恰在时段边界：立即从当前生效集合移除并重选当前时段。
		if _, ok := s.live[id]; ok {
			s.liveH.remove(id)
		}
		s.removeEvent(in.start, id, evActivate)
		s.removeEvent(in.end, id, evDeactivate)
		s.resettleCurrent()
	} else if in.start >= eff {
		// 窗口尚未开始：视同从未存在，直接摘除未来事件。
		s.removeEvent(in.start, id, evActivate)
		s.removeEvent(in.end, id, evDeactivate)
	} else if eff < in.end {
		// 窗口已开始：自生效边界起退出，原窗口终点事件被取消事件取代。
		s.events[eff] = append(s.events[eff], instrEvent{id: id, kind: evCancel})
		s.removeEvent(in.end, id, evDeactivate)
	}
	s.recomputeForecast()
	return nil
}

// removeEvent 摘除挂在边界 b 上的指定事件（不存在则忽略）。
func (s *Scheduler) removeEvent(b int64, id string, kind evKind) {
	evs, ok := s.events[b]
	if !ok {
		return
	}
	out := evs[:0]
	for _, ev := range evs {
		if ev.id != id || ev.kind != kind {
			out = append(out, ev)
		}
	}
	if len(out) == 0 {
		delete(s.events, b)
	} else {
		s.events[b] = out
	}
}

// applyEvents 应用边界 b 上的全部指令事件到真实生效集合。
func (s *Scheduler) applyEvents(b int64) {
	evs, ok := s.events[b]
	if !ok {
		return
	}
	delete(s.events, b)
	s.stats.EventsProcessed += int64(len(evs))
	applyEventsAt(evs, s.instrs, b, s.liveH)
}

// applyEventsAt 按固定顺序应用事件：先改级、再取消、再激活、最后失效，
// 保证同一边界上的事件组合结果唯一。
func applyEventsAt(evs []instrEvent, instrs map[string]*instruction, b int64, lh *levelHeap) {
	for _, ev := range evs {
		if ev.kind == evSetLevel {
			if _, ok := lh.live[ev.id]; ok {
				lh.set(ev.id, instrs[ev.id].levelAt(b))
			}
		}
	}
	for _, ev := range evs {
		if ev.kind == evCancel {
			lh.remove(ev.id)
		}
	}
	for _, ev := range evs {
		if ev.kind == evActivate {
			in := instrs[ev.id]
			if !in.cancelled || in.start < in.cancelEff {
				lh.set(ev.id, in.levelAt(in.start))
			}
		}
	}
	for _, ev := range evs {
		if ev.kind == evDeactivate {
			lh.remove(ev.id)
		}
	}
}

// recomputeForecast 从下一时段边界起重算全部未来时段的预选结果，
// 并按新旧预选差异核对通知（新增生成、落选撤回）。
// 选组模拟只涉及组堆与指令事件，开销与区域内用户数无关。
func (s *Scheduler) recomputeForecast() {
	nextB := s.nextSlotStart()
	// 模拟累计：当前时段的被限组在时段结束时会加上时段长度。
	simAcc := make(map[string]int64, len(s.accum))
	for id, a := range s.accum {
		simAcc[id] = a
	}
	for _, g := range s.curSelection {
		simAcc[g] += s.slotLen
	}
	simLive := make(map[string]int, len(s.live))
	for id, lv := range s.live {
		simLive[id] = lv
	}
	simLH := newLevelHeap(simLive, nil)
	simGH := newGroupHeap(simAcc, nil)

	newForecast := map[int64]*forecastSlot{}
	horizon := s.endH.horizon(nextB)
	for b := nextB; b < horizon; b += s.slotLen {
		if evs, ok := s.events[b]; ok {
			applyEventsAt(evs, s.instrs, b, simLH)
		}
		if k := simLH.top(); k > 0 {
			newForecast[b] = &forecastSlot{level: k, groups: simGH.selectAndCredit(k, s.slotLen)}
		}
	}
	// 对旧预测与新预测的并集逐时段核对通知。
	seen := map[int64]bool{}
	slots := make([]int64, 0, len(s.forecast)+len(newForecast))
	for b := range s.forecast {
		seen[b] = true
		slots = append(slots, b)
	}
	for b := range newForecast {
		if !seen[b] {
			slots = append(slots, b)
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, b := range slots {
		var groups []string
		if fs, ok := newForecast[b]; ok {
			groups = fs.groups
		}
		s.reconcileSlotNotifs(b, groups)
	}
	s.forecast = newForecast
}
