package settlement

import "sort"

// reading 一条累计电量读数。
type reading struct {
	t int64
	v int64
}

// monthAgg 某供电点某月账单片的增量聚合。
type monthAgg struct {
	lines             map[lineKey]*Line
	unpriceableSlices int
	unpriceableEnergy int64
}

func newMonthAgg() *monthAgg { return &monthAgg{lines: map[lineKey]*Line{}} }

// add 把一片按 sign（+1/-1）计入聚合。
func (a *monthAgg) add(s slice, sign int64) {
	if !s.priceable {
		a.unpriceableSlices += int(sign)
		a.unpriceableEnergy += sign * s.energy
		return
	}
	l, ok := a.lines[s.key]
	if !ok {
		l = &Line{DayType: s.key.dayType, SlotStart: s.key.slotStart, SlotEnd: s.key.slotEnd, Price: s.key.price}
		a.lines[s.key] = l
	}
	l.Energy += sign * s.energy
	l.Amount += sign * s.amount
	if l.Energy == 0 && l.Amount == 0 {
		delete(a.lines, s.key)
	}
}

func (a *monthAgg) empty() bool {
	return len(a.lines) == 0 && a.unpriceableSlices == 0
}

// materialize 把聚合固化为账单，行项目按（单价、日类型、时段）排序，保证可复现。
func materialize(meter string, mk monthKey, a *monthAgg, sealed bool) Bill {
	b := Bill{Meter: meter, Year: mk.year(), Month: mk.month(), Sealed: sealed}
	if a != nil {
		b.UnpriceableEnergy = a.unpriceableEnergy
		b.UnpriceableSlices = a.unpriceableSlices
		for _, l := range a.lines {
			b.Lines = append(b.Lines, *l)
		}
	}
	sort.Slice(b.Lines, func(i, j int) bool {
		x, y := b.Lines[i], b.Lines[j]
		if x.Price != y.Price {
			return x.Price < y.Price
		}
		if x.DayType != y.DayType {
			return x.DayType < y.DayType
		}
		if x.SlotStart != y.SlotStart {
			return x.SlotStart < y.SlotStart
		}
		return x.SlotEnd < y.SlotEnd
	})
	for _, l := range b.Lines {
		b.TotalEnergy += l.Energy
		b.TotalAmount += l.Amount
	}
	return b
}

// meterState 单个供电点的全部状态。
// readings 仅追加（修正/删除原地进行），登记一条读数为摊还 O(1)；
// months/sealed 以月份为键的哈希表，账单查询与已封账月份数量无关。
type meterState struct {
	readings []reading
	index    map[int64]int // 读数时刻 -> readings 下标
	months   map[monthKey]*monthAgg
	sealed   map[monthKey]*Bill
}

func newMeterState() *meterState {
	return &meterState{
		index:  map[int64]int{},
		months: map[monthKey]*monthAgg{},
		sealed: map[monthKey]*Bill{},
	}
}

// addInterval 把区间 [t0,t1) 的 e 瓦时切片后按 sign 计入涉及的各月聚合。
func (m *meterState) addInterval(t0, t1, e int64, vs *versionStore, cal *calendar, sign int64) {
	for _, s := range sliceInterval(t0, t1, e, vs, cal) {
		mk := monthOf(s.start)
		a := m.months[mk]
		if a == nil {
			a = newMonthAgg()
			m.months[mk] = a
		}
		a.add(s, sign)
	}
}

// addReading 追加一条读数；调用方已完成全部校验。
func (m *meterState) addReading(t, v int64, vs *versionStore, cal *calendar) {
	if n := len(m.readings); n > 0 {
		prev := m.readings[n-1]
		m.addInterval(prev.t, t, v-prev.v, vs, cal, +1)
	}
	m.index[t] = len(m.readings)
	m.readings = append(m.readings, reading{t: t, v: v})
}

// correct 替换下标 idx 处读数的累计电量，并重算相邻两个区间。
func (m *meterState) correct(idx int, v int64, vs *versionStore, cal *calendar) {
	old := m.readings[idx]
	hasPrev, hasNext := idx > 0, idx+1 < len(m.readings)
	if hasPrev {
		p := m.readings[idx-1]
		m.addInterval(p.t, old.t, old.v-p.v, vs, cal, -1)
	}
	if hasNext {
		nx := m.readings[idx+1]
		m.addInterval(old.t, nx.t, nx.v-old.v, vs, cal, -1)
	}
	m.readings[idx].v = v
	if hasPrev {
		p := m.readings[idx-1]
		m.addInterval(p.t, old.t, v-p.v, vs, cal, +1)
	}
	if hasNext {
		nx := m.readings[idx+1]
		m.addInterval(old.t, nx.t, nx.v-v, vs, cal, +1)
	}
}

// delete 删除下标 idx 处的读数，合并相邻区间。
func (m *meterState) delete(idx int, vs *versionStore, cal *calendar) {
	r := m.readings[idx]
	hasPrev, hasNext := idx > 0, idx+1 < len(m.readings)
	if hasPrev {
		p := m.readings[idx-1]
		m.addInterval(p.t, r.t, r.v-p.v, vs, cal, -1)
	}
	if hasNext {
		nx := m.readings[idx+1]
		m.addInterval(r.t, nx.t, nx.v-r.v, vs, cal, -1)
	}
	m.readings = append(m.readings[:idx], m.readings[idx+1:]...)
	m.index = make(map[int64]int, len(m.readings))
	for i, rd := range m.readings {
		m.index[rd.t] = i
	}
	if hasPrev && hasNext {
		p, nx := m.readings[idx-1], m.readings[idx]
		m.addInterval(p.t, nx.t, nx.v-p.v, vs, cal, +1)
	}
}

// recomputeMonth 用当前版本与节假日重算某月聚合（版本/节假日变更时调用）。
func (m *meterState) recomputeMonth(mk monthKey, vs *versionStore, cal *calendar) {
	a := newMonthAgg()
	start, end := mk.start(), mk.end()
	n := len(m.readings)
	if n >= 2 {
		lo := sort.Search(n, func(i int) bool { return m.readings[i].t > start }) - 1
		if lo < 0 {
			lo = 0
		}
		hi := sort.Search(n, func(i int) bool { return m.readings[i].t >= end })
		for j := lo; j+1 < n && j < hi; j++ {
			r0, r1 := m.readings[j], m.readings[j+1]
			if r1.t <= start || r0.t >= end {
				continue
			}
			for _, s := range sliceInterval(r0.t, r1.t, r1.v-r0.v, vs, cal) {
				if s.start >= start && s.start < end {
					a.add(s, +1)
				}
			}
		}
	}
	if a.empty() {
		delete(m.months, mk)
	} else {
		m.months[mk] = a
	}
}

// recomputeFrom 重算所有不早于 mk 的未封账月份（新版本登记后调用）。
func (m *meterState) recomputeFrom(mk monthKey, vs *versionStore, cal *calendar) {
	var keys []monthKey
	for k := range m.months {
		if k >= mk {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		m.recomputeMonth(k, vs, cal)
	}
}

// seal 把某月聚合固化为已封账账单。
func (m *meterState) seal(meterID string, mk monthKey) {
	b := materialize(meterID, mk, m.months[mk], true)
	m.sealed[mk] = &b
	delete(m.months, mk)
}

// bill 查询某月账单：已封账返回冻结快照，否则反映当前最新状态。
func (m *meterState) bill(meterID string, mk monthKey) Bill {
	if b, ok := m.sealed[mk]; ok {
		cp := *b
		cp.Lines = append([]Line(nil), b.Lines...)
		return cp
	}
	return materialize(meterID, mk, m.months[mk], false)
}

func (m *meterState) monthSealed(mk monthKey) bool {
	_, ok := m.sealed[mk]
	return ok
}

// rangeSealed 判断修正/删除读数受影响的范围（相邻区间 [lo,hi) 与该时刻所在月）
// 是否触及任何已封账月份。
func (m *meterState) rangeSealed(lo, hi, t int64) bool {
	if m.monthSealed(monthOf(t)) {
		return true
	}
	if hi > lo {
		for mk := monthOf(lo); mk <= monthOf(hi-1); mk++ {
			if m.monthSealed(mk) {
				return true
			}
		}
	}
	return false
}
