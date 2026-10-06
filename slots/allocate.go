package slots

// segTree 稀疏场景下的区间最大值线段树（数组实现），
// 点更新与区间最大值查询均为 O(log W)，W 为航季总周数。
type segTree struct {
	size  int
	max   []int
	stats *Stats
}

func newSegTree(n int, stats *Stats) *segTree {
	size := 1
	for size < n {
		size <<= 1
	}
	return &segTree{size: size, max: make([]int, 2*size), stats: stats}
}

func (t *segTree) add(idx, delta int) {
	i := idx + t.size
	t.max[i] += delta
	t.stats.TreeNodeVisits++
	for i > 1 {
		i >>= 1
		if t.max[2*i] > t.max[2*i+1] {
			t.max[i] = t.max[2*i]
		} else {
			t.max[i] = t.max[2*i+1]
		}
		t.stats.TreeNodeVisits++
	}
}

// rangeMax 返回闭区间 [l, r] 上的最大占用数。
func (t *segTree) rangeMax(l, r int) int {
	l += t.size
	r += t.size
	m := 0
	for l <= r {
		t.stats.TreeNodeVisits++
		if l%2 == 1 {
			if t.max[l] > m {
				m = t.max[l]
			}
			l++
		}
		if r%2 == 0 {
			if t.max[r] > m {
				m = t.max[r]
			}
			r--
		}
		l >>= 1
		r >>= 1
	}
	return m
}

func (e *Engine) slotTree(slot SlotKey) *segTree {
	t, ok := e.trees[slot]
	if !ok {
		t = newSegTree(e.cfg.TotalWeeks, &e.stats)
		e.trees[slot] = t
	}
	return t
}

// canAllocateCell O(1) 判定某周某天某小时段能否再分配（恰等于上限时不可再分配）。
func (e *Engine) canAllocateCell(week, weekday, hour int) bool {
	e.stats.CellReads++
	return e.occ[CellKey{Week: week, Weekday: weekday, Hour: hour}] < e.cfg.CapacityPerSlot
}

// rangeFree O(log W) 判定某小时段在周范围 [s, e] 内每周都有剩余容量。
func (e *Engine) rangeFree(weekday, hour, s, end int) bool {
	return e.slotTree(SlotKey{Weekday: weekday, Hour: hour}).rangeMax(s, end) < e.cfg.CapacityPerSlot
}

// materialize 把申请转为系列并占用其覆盖的每个单元格。
func (e *Engine) materialize(req *Request) *Series {
	s := &Series{
		ID: e.nextSeriesID, Airline: req.Airline,
		Weekday: req.Weekday, Hour: req.Hour,
		StartWeek: req.StartWeek, EndWeek: req.EndWeek,
		Weeks: map[int]WeekState{},
	}
	e.nextSeriesID++
	e.series[s.ID] = s
	e.seriesOrder = append(e.seriesOrder, s.ID)
	tr := e.slotTree(SlotKey{Weekday: req.Weekday, Hour: req.Hour})
	for w := req.StartWeek; w <= req.EndWeek; w++ {
		e.occ[CellKey{Week: w, Weekday: req.Weekday, Hour: req.Hour}]++
		tr.add(w, 1)
	}
	return s
}

// checkHistoricFeasible 校验历史申请合计不超过任何单元格容量（全有或全无的前置条件）。
func (e *Engine) checkHistoricFeasible() error {
	need := map[CellKey]int{}
	for _, r := range e.requests {
		if !r.Historic {
			continue
		}
		for w := r.StartWeek; w <= r.EndWeek; w++ {
			c := CellKey{Week: w, Weekday: r.Weekday, Hour: r.Hour}
			need[c]++
			if need[c] > e.cfg.CapacityPerSlot {
				return reject(ReasonInvalidParam, "历史申请在单元格 %+v 超过容量 %d", c, e.cfg.CapacityPerSlot)
			}
		}
	}
	return nil
}

// runAllocation 截止时一次性结算：历史优先 -> 新进入者保留额 -> 其余申请 -> 等候名单。
func (e *Engine) runAllocation() {
	// 第一遍：满足全部历史优先权申请（可行性已由 checkHistoricFeasible 保证）。
	held := map[string]int{}
	for _, r := range e.requests {
		if r.Historic {
			e.materialize(r)
			held[r.Airline]++
		}
	}

	// 新进入者保留额：此时剩余容量的一半（向下取整）。惰性计算等价于统一快照，
	// 因为单元格在首次被触及前剩余容量未变。
	reserve := map[CellKey]int{}
	reserveOf := func(c CellKey) int {
		if v, ok := reserve[c]; ok {
			return v
		}
		v := (e.cfg.CapacityPerSlot - e.occ[c]) / 2
		reserve[c] = v
		return v
	}

	// 第二遍：新进入者申请按提交时刻早者先满足，消耗保留额。
	var rest []*Request
	for _, r := range e.requests {
		if r.Historic {
			continue
		}
		if held[r.Airline] >= e.cfg.NewcomerThreshold {
			rest = append(rest, r)
			continue
		}
		ok := true
		for w := r.StartWeek; w <= r.EndWeek && ok; w++ {
			if reserveOf(CellKey{Week: w, Weekday: r.Weekday, Hour: r.Hour}) <= 0 {
				ok = false
			}
		}
		if !ok {
			rest = append(rest, r)
			continue
		}
		e.materialize(r)
		held[r.Airline]++
		for w := r.StartWeek; w <= r.EndWeek; w++ {
			c := CellKey{Week: w, Weekday: r.Weekday, Hour: r.Hour}
			if v := reserveOf(c); v > 0 {
				reserve[c] = v - 1 // 减一且不低于零
			}
		}
	}

	// 第三遍：其余申请按提交时刻早者先处理，要求剩余容量减去保留额仍大于零。
	for _, r := range rest {
		ok := true
		for w := r.StartWeek; w <= r.EndWeek && ok; w++ {
			c := CellKey{Week: w, Weekday: r.Weekday, Hour: r.Hour}
			if e.cfg.CapacityPerSlot-e.occ[c]-reserveOf(c) <= 0 {
				ok = false
			}
		}
		if ok {
			e.materialize(r)
		} else {
			slot := SlotKey{Weekday: r.Weekday, Hour: r.Hour}
			e.waitlist[slot] = append(e.waitlist[slot], r)
		}
	}
}

// promote 返还释放某单元格后，按等候名单次序把容量分配给覆盖该周且仍有效的申请。
// 开销为 O(名单长度 * log W)，与机场内系列总数无关。
func (e *Engine) promote(slot SlotKey, week int) {
	list := e.waitlist[slot]
	if len(list) == 0 {
		return
	}
	kept := list[:0]
	for _, req := range list {
		if req.StartWeek <= week && week <= req.EndWeek &&
			e.rangeFree(req.Weekday, req.Hour, req.StartWeek, req.EndWeek) {
			e.materialize(req)
		} else {
			kept = append(kept, req)
		}
	}
	for i := len(kept); i < len(list); i++ {
		list[i] = nil
	}
	e.waitlist[slot] = kept
}
