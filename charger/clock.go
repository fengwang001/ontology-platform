package charger

import "sort"

// advanceLocked 把时钟从 st.now 推进到 target，返回区间内充满事件。
// 复杂度只与在站车辆数和本区间充满/上限变更次数相关，不逐秒遍历：
// 充电功率在相邻事件之间恒定，区间电量按 功率×时长 一次性累计。
//
// 每个切分时刻的处理次序固定：
// 先按恒定功率累计到该时刻并判定充满（同时刻多车按插枪先后逐一处理，
// 每台充满立即重分），再生效同一时刻的总上限变更并重分。
func (st *Station) advanceLocked(target int64) []FillEvent {
	var events []FillEvent

	for st.now < target {
		next := st.nextBoundaryLocked(target)
		if next > target {
			st.accrueLocked(target - st.now)
			st.now = target
			break
		}

		st.accrueLocked(next - st.now)
		st.now = next

		// 该时刻充满的车辆按插枪先后逐台处理，每台触发一次重新分配。
		var filling []*Session
		for _, s := range st.portSess {
			if s.State == StateCharging && s.Charged >= s.Need {
				filling = append(filling, s)
			}
		}
		sort.SliceStable(filling, func(i, j int) bool {
			if filling[i].PlugAt != filling[j].PlugAt {
				return filling[i].PlugAt < filling[j].PlugAt
			}
			return filling[i].ID < filling[j].ID
		})
		for _, s := range filling {
			s.Charged = s.Need
			s.Power = 0
			s.State = StateFull
			s.FullAt = st.now
			events = append(events, FillEvent{SessionID: s.ID, At: st.now})
			st.reallocateLocked()
		}

		if st.applyChangesLocked() {
			st.reallocateLocked()
		}
	}

	// 处理推进起点已到期的预约（如 at 恰等于当前时刻的上限变更）。
	if st.applyChangesLocked() {
		st.reallocateLocked()
	}
	return events
}

// applyChangesLocked 生效所有 at<=now 的预约上限变更（按登记次序）。
// 返回是否至少生效了一条。
func (st *Station) applyChangesLocked() bool {
	kept := make([]capChange, 0, len(st.changes))
	applied := false
	for _, c := range st.changes {
		if c.at <= st.now {
			st.totalCap = c.cap
			applied = true
		} else {
			kept = append(kept, c)
		}
	}
	st.changes = kept
	return applied
}

// nextBoundaryLocked 找出 (now, target] 内最近的事件时刻：
// 最近的预约上限变更，或某台充电中车辆按当前功率充满的时刻。
// 没有事件时返回 target+1。
func (st *Station) nextBoundaryLocked(target int64) int64 {
	next := target + 1
	for _, c := range st.changes {
		if c.at > st.now && c.at <= target && c.at < next {
			next = c.at
		}
	}
	for _, s := range st.portSess {
		if s.State != StateCharging || s.Power <= 0 {
			continue
		}
		rem := s.Need - s.Charged
		// 充满所需整秒数：ceil(rem/Power)。
		ticks := int64((rem + s.Power - 1) / s.Power)
		at := st.now + ticks
		if at <= target && at < next {
			next = at
		}
	}
	return next
}

// accrueLocked 让所有充电中车辆以当前功率累计 d 秒电量，
// 单台累计不得超过需求电量（充满切片点的整数夹取）。
func (st *Station) accrueLocked(d int64) {
	if d <= 0 {
		return
	}
	for _, s := range st.portSess {
		if s.State != StateCharging {
			continue
		}
		gain := s.Power * int(d)
		s.Charged += gain
		if s.Charged > s.Need {
			s.Charged = s.Need
		}
	}
}
