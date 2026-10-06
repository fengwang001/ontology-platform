package imaging

import "sort"

// kidneyDecision 以某预约开始时刻 start 判定肾功能。
// 最新结果采样晚于 start（预约开始后才采样）时视同无有效结果。
type kidneyDecision struct {
	needHydration bool
	err           error
}

func (h *Hospital) assessKidney(p *patient, start int) kidneyDecision {
	latest := p.latest
	if latest.order == 0 || latest.sampledAt > start {
		return kidneyDecision{err: ErrKidneyMissing}
	}
	validity := h.cfg.ValidityNormal
	if p.highRisk {
		validity = h.cfg.ValidityHighRisk
	}
	// 恰等于有效期仍有效。
	if start-latest.sampledAt > validity {
		return kidneyDecision{err: ErrKidneyMissing}
	}
	if latest.value < h.cfg.KidneyLow {
		return kidneyDecision{err: ErrKidneyInsufficient}
	}
	return kidneyDecision{needHydration: latest.value < h.cfg.KidneyHigh}
}

// qcConflict 判断 [start,end) 是否与设备每日重复质控时段重叠（半开，恰相接不冲突）。
func qcConflict(qcs []Interval, start, end int) bool {
	if len(qcs) == 0 {
		return false
	}
	firstDay := start / dayMin
	lastDay := (end - 1) / dayMin
	for day := firstDay; day <= lastDay; day++ {
		dayStart := day * dayMin
		segLo := start - dayStart
		if segLo < 0 {
			segLo = 0
		}
		segHi := end - dayStart
		if segHi > dayMin {
			segHi = dayMin
		}
		for _, q := range qcs {
			if segLo < q.End && q.Start < segHi {
				return true
			}
		}
	}
	return false
}

// countAt 返回时刻 t（按半开语义，即 [t,t+1) 起点）的留观人数。
func (h *Hospital) countAt(t int) int {
	return h.obs.starts.countLE(t) - h.obs.ends.countLE(t)
}

// capacityOK 校验加入半开留观区间 [s,e) 后任意时刻人数不超过容量。
// 只枚举受影响窗口 [s,e) 内的开始/结束事件；事件数由既有合法占用上界，
// 故开销与窗口外的历史留观总数无关。
func (h *Hospital) capacityOK(s, e int) bool {
	type ev struct {
		t int
		d int
	}
	var events []ev
	for _, t := range h.obs.starts.rangeKeys(s, e-1) {
		events = append(events, ev{t, 1})
	}
	for _, t := range h.obs.ends.rangeKeys(s, e-1) {
		events = append(events, ev{t, -1})
	}
	// 同刻先减后加：半开区间恰相接（前例结束刻 == 后例开始刻）合法。
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		return events[i].d < events[j].d
	})
	// s 时刻：此前已在场人数 + 新区间。
	active := h.countAt(s-1) + 1
	if active > h.cfg.ObservationCapacity {
		return false
	}
	for _, x := range events {
		active += x.d
		if active > h.cfg.ObservationCapacity {
			return false
		}
	}
	return true
}

func (h *Hospital) addObservation(s, e int) {
	h.obs.starts.insert(s, int64(s))
	h.obs.ends.insert(e, int64(e))
}

func (h *Hospital) removeObservation(s, e int) {
	h.obs.starts.erase(s)
	h.obs.ends.erase(e)
}

type capacityEvent struct {
	t int
	d int
}

// sweepCapacity 在给定初始人数与事件序列下扫描容量。
// 同刻事件须已按 -1 先于 +1 排序（半开相接合法）。
func sweepCapacity(events []capacityEvent, initial, capacity int) bool {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		return events[i].d < events[j].d
	})
	active := initial
	if active > capacity {
		return false
	}
	for _, e := range events {
		active += e.d
		if active > capacity {
			return false
		}
	}
	return true
}
