package billing

import (
	"sort"
	"strconv"
	"sync"
)

// Building 是并发安全的楼宇抄表记账服务。
// 一把互斥锁串行化所有变更操作：结果等价于某个合法串行顺序，
// 同一只表不可能产生两条同时刻读数。只读查询同样取锁得到一致快照。
type Building struct {
	mu      sync.Mutex
	now     int64
	estGap  int64
	master  *Meter
	meters  map[string]*Meter
	house   map[string]*Household
	order   []*Household
	bills   []*Bill // 已结算账期，起点升序、首尾相接
	correct []Correction
	current map[[2]int64]*billCurrent
	journal []Op
}

// Op 是可重放的操作日志（朴素模型与随机对照测试使用）。
type Op struct {
	Kind          string
	Now           int64
	Meter         string
	House         string
	Value, Value2 int64
	Cap           int64
	Est           bool
	P0, P1        int64
	Area          int64
	S, E          int64
	Accepted      bool
	Code          ErrCode
}

// NewBuilding 创建楼宇。estGap 即题面 G：距上次实抄“严格超过”该单位数才可估抄。
func NewBuilding(estGap int64) *Building {
	if estGap < 0 {
		estGap = 0
	}
	return &Building{estGap: estGap, meters: map[string]*Meter{}, house: map[string]*Household{}}
}

func validate(ok bool) error {
	if !ok {
		return errf(ErrInvalidArgument, "invalid parameter")
	}
	return nil
}

func (b *Building) clock(now int64) error {
	if now < 0 {
		return errf(ErrInvalidArgument, "negative now %d", now)
	}
	if now < b.now {
		return errf(ErrClockRollback, "now %d < last accepted %d", now, b.now)
	}
	return nil
}

func (b *Building) fail(o Op, err error) error {
	o.Accepted = false
	if e, ok := err.(*Error); ok {
		o.Code = e.Code
	}
	b.journal = append(b.journal, o)
	return err
}

func (b *Building) failLocked(o Op, err error) error {
	b.journal = append(b.journal, markReject(o, err))
	return err
}

func (b *Building) logLocked(o Op) {
	o.Accepted = true
	b.journal = append(b.journal, o)
}

func markReject(o Op, err error) Op {
	o.Accepted = false
	if e, ok := err.(*Error); ok {
		o.Code = e.Code
	}
	return o
}

// AddMeter 注册表（master=true 为总表）。capacity 为量程上限。
func (b *Building) AddMeter(id string, capacity int64, master bool, now int64) error {
	o := Op{Kind: "add_meter", Now: now, Meter: id, Cap: capacity, Est: master}
	if err := validate(id != "" && capacity > 0 && now >= 0); err != nil {
		return b.fail(o, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.clock(now); err != nil {
		return b.failLocked(o, err)
	}
	if _, exists := b.meters[id]; exists {
		return b.failLocked(o, errf(ErrInvalidArgument, "meter %q already exists", id))
	}
	m := &Meter{id: id, cap: capacity}
	b.meters[id] = m
	if master {
		b.master = m
	}
	b.now = now
	b.logLocked(o)
	return nil
}

// AddHousehold 注册一户（面积 area，绑定表 meterID）。
func (b *Building) AddHousehold(id, meterID string, area int64, now int64) error {
	o := Op{Kind: "add_household", Now: now, Meter: meterID, House: id, Area: area}
	if err := validate(id != "" && meterID != "" && area > 0 && now >= 0); err != nil {
		return b.fail(o, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.clock(now); err != nil {
		return b.failLocked(o, err)
	}
	if _, ok := b.house[id]; ok {
		return b.failLocked(o, errf(ErrInvalidArgument, "household %q already exists", id))
	}
	m, ok := b.meters[meterID]
	if !ok {
		return b.failLocked(o, errf(ErrMeterNotFound, "meter %q", meterID))
	}
	h := &Household{id: id, area: area, meter: m}
	b.house[id] = h
	b.order = append(b.order, h)
	b.now = now
	b.logLocked(o)
	return nil
}

// AddOccupancy 增加左闭右开在住记录 [start,end)，不允许与既有记录重叠。
func (b *Building) AddOccupancy(houseID string, start, end, now int64) error {
	o := Op{Kind: "add_occupancy", Now: now, House: houseID, S: start, E: end}
	if err := validate(houseID != "" && start >= 0 && end > start && now >= 0); err != nil {
		return b.fail(o, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.clock(now); err != nil {
		return b.failLocked(o, err)
	}
	h, ok := b.house[houseID]
	if !ok {
		return b.failLocked(o, errf(ErrInvalidArgument, "household %q not found", houseID))
	}
	for _, x := range h.occupancy {
		if start < x.End && x.Start < end {
			return b.failLocked(o, errf(ErrInvalidArgument, "occupancy overlaps existing record"))
		}
	}
	h.occupancy = append(h.occupancy, Occupancy{Start: start, End: end})
	sort.Slice(h.occupancy, func(i, j int) bool { return h.occupancy[i].Start < h.occupancy[j].Start })
	b.now = now
	b.logLocked(o)
	return nil
}

// ReadingInput 是读数录入参数。
type ReadingInput struct {
	MeterID   string
	Time      int64
	Value     int64
	Estimated bool
	Now       int64
}

// EnterReading 录入读数；返回值保留给调用方获知被替代估抄数量（通常不使用）。
func (b *Building) EnterReading(in ReadingInput) (replaced int, err error) {
	o := Op{Kind: "enter_reading", Now: in.Now, Meter: in.MeterID, Value: in.Value, P0: in.Time, Est: in.Estimated}
	if in.MeterID == "" {
		return 0, b.fail(o, errf(ErrInvalidArgument, "empty meter id"))
	}
	if in.Time < 0 || in.Value < 0 {
		return 0, b.fail(o, errf(ErrInvalidArgument, "negative time or value"))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.clock(in.Now); err != nil {
		return 0, b.failLocked(o, err)
	}
	m, ok := b.meters[in.MeterID]
	if !ok {
		return 0, b.failLocked(o, errf(ErrMeterNotFound, "meter %q", in.MeterID))
	}
	var removed []*Reading
	if in.Estimated {
		if err := b.checkEstimate(m, in); err != nil {
			return 0, b.failLocked(o, err)
		}
	} else {
		idx := sort.Search(len(m.readings), func(i int) bool { return m.readings[i].Time >= in.Time })
		if idx < len(m.readings) && m.readings[idx].Time == in.Time {
			return 0, b.failLocked(o, errf(ErrReadingOutOfOrder, "reading at %d already exists", in.Time))
		}
		if in.Value > m.cap {
			return 0, b.failLocked(o, errf(ErrInvalidArgument, "value %d over capacity %d", in.Value, m.cap))
		}
		if err := checkIllegalAround(m, in.Time, in.Value, idx); err != nil {
			return 0, b.failLocked(o, err)
		}
		for len(m.readings) > 0 {
			last := m.readings[len(m.readings)-1]
			if !last.Estimated || last.Time >= in.Time {
				break
			}
			removed = append(removed, last)
			m.readings = m.readings[:len(m.readings)-1]
		}
	}
	affectedFrom := in.Time
	k := sort.Search(len(m.readings), func(i int) bool { return m.readings[i].Time > in.Time }) - 1
	if k >= 0 && m.readings[k].Time < in.Time {
		affectedFrom = m.readings[k].Time
	}
	// 估抄被替代：被移除估抄之前的跨度也改变，起点回退到第一个被移除估抄的前锚点。
	if len(removed) > 0 {
		firstRemovedTime := removed[len(removed)-1].Time
		if k := sort.Search(len(m.readings), func(i int) bool { return m.readings[i].Time >= firstRemovedTime }) - 1; k >= 0 {
			if t := m.readings[k].Time; t < affectedFrom {
				affectedFrom = t
			}
		}
	}
	affected := b.affectedBills(affectedFrom, in.Time)
	mutate := func(t *Meter) {
		r := &Reading{Time: in.Time, Value: in.Value, Estimated: in.Estimated, cap: t.cap}
		if in.Estimated {
			t.appendEstimate(r)
		} else {
			idx := sort.Search(len(t.readings), func(i int) bool { return t.readings[i].Time >= in.Time })
			t.insertActual(idx, r)
		}
	}
	trial, results, err := b.applyTrial(m, mutate, affected)
	if err != nil {
		// 试算在克隆表上进行；原表只做了“移除估抄”，按时间升序接回尾部。
		for i := len(removed) - 1; i >= 0; i-- {
			m.readings = append(m.readings, removed[i])
		}
		return 0, b.failLocked(o, err)
	}
	// 提交：用试算表序列（已移除被替代估抄并插入新读数）替换原序列。
	b.commitTrial(m, trial)
	if len(affected) > 0 {
		reason := "actual reading backfill"
		if len(removed) > 0 {
			reason = "estimated reading replaced by actual reading at " + strconv.FormatInt(in.Time, 10)
		}
		b.emitCorrections(affected, results, reason)
	}
	b.now = in.Now
	b.logLocked(o)
	return len(removed), nil
}

// ChangeMeter 在 now 时刻换表：旧表终读数 oldFinal，新表量程 newCap、起始 newStart。
func (b *Building) ChangeMeter(meterID string, now, oldFinal, newStart, newCap int64) error {
	o := Op{Kind: "change_meter", Now: now, Meter: meterID, Value: oldFinal, Value2: newStart, Cap: newCap, P0: now}
	if err := validate(meterID != "" && now >= 0 && newCap > 0 && oldFinal >= 0 && newStart >= 0); err != nil {
		return b.fail(o, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.clock(now); err != nil {
		return b.failLocked(o, err)
	}
	m, ok := b.meters[meterID]
	if !ok {
		return b.failLocked(o, errf(ErrMeterNotFound, "meter %q", meterID))
	}
	if oldFinal > m.cap || newStart > newCap {
		return b.failLocked(o, errf(ErrInvalidArgument, "reading over capacity"))
	}
	if len(m.readings) > 0 {
		last := m.readings[len(m.readings)-1]
		if last.Time > now {
			return b.failLocked(o, errf(ErrReadingOutOfOrder, "change time %d before last reading", now))
		}
		if last.Time == now {
			// 当刻已有读数：它必须就是旧表终读数（此时无需再造合成点，
			// 只登记新表段）。
			if last.Value != oldFinal || last.segStart {
				return b.failLocked(o, errf(ErrReadingIllegal,
					"final reading at change time must equal oldFinal %d", oldFinal))
			}
		} else if legalReading(last.Value, oldFinal, last.cap) {
			return b.failLocked(o, errf(ErrReadingIllegal, "final reading drop exceeds half capacity"))
		}
	}
	affected := b.affectedBills(now, now)
	trial, results, err := b.applyTrial(m, func(t *Meter) {
		t.startNewSegment(now, oldFinal, newCap, newStart)
	}, affected)
	if err != nil {
		return b.failLocked(o, err)
	}
	b.commitTrial(m, trial)
	if len(affected) > 0 {
		b.emitCorrections(affected, results, "meter change at "+strconv.FormatInt(now, 10))
	}
	b.now = now
	b.logLocked(o)
	return nil
}

// Settle 结算账期 [start,end)：账期须首尾相接且不可重复结算。
func (b *Building) Settle(start, end, now int64) (*Settlement, error) {
	o := Op{Kind: "settle", Now: now, P0: start, P1: end}
	if err := validate(start >= 0 && end > start && now >= 0); err != nil {
		return nil, b.fail(o, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.clock(now); err != nil {
		return nil, b.failLocked(o, err)
	}
	if b.findBill(start, end) != nil {
		return nil, b.failLocked(o, errf(ErrPeriodAlreadySettled, "period [%d,%d)", start, end))
	}
	if len(b.bills) > 0 && b.bills[len(b.bills)-1].Period[1] != start {
		return nil, b.failLocked(o, errf(ErrInvalidArgument,
			"period [%d,%d) not adjacent to last settled end %d", start, end, b.bills[len(b.bills)-1].Period[1]))
	}
	bill, err := b.buildBill(start, end)
	if err != nil {
		return nil, b.failLocked(o, err)
	}
	b.bills = append(b.bills, bill)
	// 新边界确定后，末端恰在 start 的读数跨度余量完成归属；
	// 重算此前账期并为发生变化者生成更正（不改动新、旧账单本身）。
	b.emitDeferredCorrections(start, bill)
	b.now = now
	b.logLocked(o)
	return billToSettlement(bill), nil
}

// emitDeferredCorrections 处理余量递延导致的历史账期用量变化。
func (b *Building) emitDeferredCorrections(start int64, fresh *Bill) {
	deferred := b.deferredMeters(start, fresh.Period[1])
	if len(deferred) == 0 {
		return
	}
	for i := 0; i < len(b.bills)-1; i++ {
		bill := b.bills[i]
		rc, err := b.recomputeBill(bill, nil, fresh)
		if err != nil || !rc.ok {
			continue // 理论上不会发生：新账单已成功
		}
		cur := b.ensureCurrent(bill)
		changed := false
		for j := range cur.self {
			if cur.self[j] != rc.self[j] || cur.share[j] != rc.shared[j] {
				changed = true
			}
		}
		if !changed {
			continue
		}
		c := Correction{Period: bill.Period,
			Reason: "linear attribution remainder finalized at boundary " + strconv.FormatInt(start, 10),
			Deltas: map[string]int64{}}
		var newShared int64
		for j, id := range bill.ids {
			c.Deltas[id] = (rc.self[j] + rc.shared[j]) - (cur.self[j] + cur.share[j])
			newShared += rc.shared[j]
		}
		c.SharedDelta = newShared - sum64(cur.share)
		b.correct = append(b.correct, c)
		cur.self = rc.self
		cur.share = rc.shared
		cur.master = rc.master
		cur.sharedTotal = rc.sharedTotal
	}
}

// emitReadingCorrections 与 emitCorrections 相同，但保证在无变化时不产生更正。

// Corrections 返回全部更正（生成顺序）。
func (b *Building) Corrections() []Correction {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Correction, len(b.correct))
	copy(out, b.correct)
	return out
}

// Bills 返回已结算账期（起点升序）。
func (b *Building) Bills() [][2]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([][2]int64, len(b.bills))
	for i, x := range b.bills {
		out[i] = x.Period
	}
	return out
}

// Journal 返回操作日志副本（测试/朴素模型重放）。
func (b *Building) Journal() []Op {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Op, len(b.journal))
	copy(out, b.journal)
	return out
}
