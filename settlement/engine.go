package settlement

import "sync"

// Engine 分时电价结算引擎。所有公开方法可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个串行执行顺序。
type Engine struct {
	mu     sync.Mutex
	vs     versionStore
	cal    *calendar
	meters map[string]*meterState

	maxSealedMonthEnd int64            // 所有已封账月份月末的最大值，0 表示无
	sealedMonths      map[monthKey]int // 月份 -> 已封该月的供电点数
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{
		cal:          newCalendar(),
		meters:       map[string]*meterState{},
		sealedMonths: map[monthKey]int{},
	}
}

func checkMeterParam(op, meter string) *Error {
	if meter == "" {
		return fail(KindInvalidParam, op, "供电点为空")
	}
	return nil
}

func checkTimeParam(op string, t int64) *Error {
	if t < MinTime || t >= MaxTime {
		return fail(KindInvalidParam, op, "时刻越界: %d", t)
	}
	return nil
}

func checkCumulativeParam(op string, v int64) *Error {
	if v < 0 || v > MaxCumulative {
		return fail(KindInvalidParam, op, "电量越界: %d", v)
	}
	return nil
}

// AddReading 登记一条读数（时刻，累计电量）。同一供电点的读数时刻必须严格
// 递增、累计电量必须不减。拒绝次序：参数非法 > 月份已封账 > 时序错误 > 读数倒退。
func (e *Engine) AddReading(meter string, t, v int64) error {
	const op = "AddReading"
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkMeterParam(op, meter); err != nil {
		return err
	}
	if err := checkTimeParam(op, t); err != nil {
		return err
	}
	if err := checkCumulativeParam(op, v); err != nil {
		return err
	}
	m := e.meters[meter]
	if m != nil && m.monthSealed(monthOf(t)) {
		return fail(KindMonthSealed, op, "月份 %s 已封账", monthOf(t))
	}
	if m != nil && len(m.readings) > 0 {
		last := m.readings[len(m.readings)-1]
		if t <= last.t {
			return fail(KindOutOfOrder, op, "时刻 %d 不晚于最新读数时刻 %d", t, last.t)
		}
		if v < last.v {
			return fail(KindReadingRollback, op, "累计电量 %d 小于前一读数 %d", v, last.v)
		}
	}
	if m == nil {
		m = newMeterState()
		e.meters[meter] = m
	}
	m.addReading(t, v, &e.vs, e.cal)
	return nil
}

// findReading 定位读数；不存在时报参数非法。
func (e *Engine) findReading(op, meter string, t int64) (*meterState, int, *Error) {
	m := e.meters[meter]
	if m == nil {
		return nil, 0, fail(KindInvalidParam, op, "供电点 %q 无读数", meter)
	}
	idx, ok := m.index[t]
	if !ok {
		return nil, 0, fail(KindInvalidParam, op, "时刻 %d 无既有读数", t)
	}
	return m, idx, nil
}

// CorrectReading 替换未封账月份内某个已有读数时刻的累计电量，
// 新值必须与前后相邻读数保持不减关系；成功后相邻两区间重新切片计价。
func (e *Engine) CorrectReading(meter string, t, v int64) error {
	const op = "CorrectReading"
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkMeterParam(op, meter); err != nil {
		return err
	}
	if err := checkTimeParam(op, t); err != nil {
		return err
	}
	if err := checkCumulativeParam(op, v); err != nil {
		return err
	}
	m, idx, err := e.findReading(op, meter, t)
	if err != nil {
		return err
	}
	lo, hi := t, t
	if idx > 0 {
		lo = m.readings[idx-1].t
	}
	if idx+1 < len(m.readings) {
		hi = m.readings[idx+1].t
	}
	if m.rangeSealed(lo, hi, t) {
		return fail(KindMonthSealed, op, "读数或相邻区间触及已封账月份")
	}
	if idx > 0 && v < m.readings[idx-1].v {
		return fail(KindReadingRollback, op, "累计电量 %d 小于前一读数 %d", v, m.readings[idx-1].v)
	}
	if idx+1 < len(m.readings) && v > m.readings[idx+1].v {
		return fail(KindReadingRollback, op, "累计电量 %d 大于后一读数 %d", v, m.readings[idx+1].v)
	}
	m.correct(idx, v, &e.vs, e.cal)
	return nil
}

// DeleteReading 删除未封账月份内某个已有读数时刻的读数，相邻区间合并重算。
func (e *Engine) DeleteReading(meter string, t int64) error {
	const op = "DeleteReading"
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkMeterParam(op, meter); err != nil {
		return err
	}
	if err := checkTimeParam(op, t); err != nil {
		return err
	}
	m, idx, err := e.findReading(op, meter, t)
	if err != nil {
		return err
	}
	lo, hi := t, t
	if idx > 0 {
		lo = m.readings[idx-1].t
	}
	if idx+1 < len(m.readings) {
		hi = m.readings[idx+1].t
	}
	if m.rangeSealed(lo, hi, t) {
		return fail(KindMonthSealed, op, "读数或相邻区间触及已封账月份")
	}
	m.delete(idx, &e.vs, e.cal)
	return nil
}

// RegisterVersion 登记一个电价表版本。时段表必须按三种日类型分别无缝覆盖
// 整日；生效时刻不得与已有版本重复；生效时刻落在任何已封账月份之内或之前
// 时报「月份已封账」。成功后所有不早于生效月份的未封账账单重新计算。
func (e *Engine) RegisterVersion(eff int64, schedules map[DayType][]Slot) error {
	const op = "RegisterVersion"
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkTimeParam(op, eff); err != nil {
		return err
	}
	sched, err := validateSchedules(schedules)
	if err != nil {
		return fail(KindInvalidParam, op, "%v", err)
	}
	if _, dup := e.vs.find(eff); dup {
		return fail(KindInvalidParam, op, "生效时刻 %d 与已有版本重复", eff)
	}
	if eff < e.maxSealedMonthEnd {
		return fail(KindMonthSealed, op, "生效时刻落在已封账月份之内或之前")
	}
	e.vs.add(eff, sched)
	mk := monthOf(eff)
	for _, m := range e.meters {
		m.recomputeFrom(mk, &e.vs, e.cal)
	}
	return nil
}

// RegisterHoliday 登记一个日期为节假日（"2006-01-02"）。
func (e *Engine) RegisterHoliday(date string) error {
	return e.setHoliday("RegisterHoliday", date, true)
}

// UnregisterHoliday 撤销一个日期的节假日登记。
func (e *Engine) UnregisterHoliday(date string) error {
	return e.setHoliday("UnregisterHoliday", date, false)
}

func (e *Engine) setHoliday(op, date string, on bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	key, dayStart, err := parseDate(date)
	if err != nil {
		return fail(KindInvalidParam, op, "%v", err)
	}
	mk := monthOf(dayStart)
	if e.sealedMonths[mk] > 0 {
		return fail(KindMonthSealed, op, "月份 %s 已封账", mk)
	}
	if on == e.cal.holidays[key] {
		return nil // 幂等
	}
	e.cal.holidays[key] = on
	for _, m := range e.meters {
		if _, ok := m.months[mk]; ok {
			m.recomputeMonth(mk, &e.vs, e.cal)
		}
	}
	return nil
}

// SealMonth 封账某供电点某月。要求：该月所有片均可计价，且存在一条时刻
// 不早于该月末的读数。重复封账为幂等成功。
func (e *Engine) SealMonth(meter string, year, month int) error {
	const op = "SealMonth"
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkMeterParam(op, meter); err != nil {
		return err
	}
	mk, err := checkYearMonth(year, month)
	if err != nil {
		return fail(KindInvalidParam, op, "%v", err)
	}
	m := e.meters[meter]
	if m != nil && m.monthSealed(mk) {
		return nil
	}
	if m == nil || len(m.readings) == 0 || m.readings[len(m.readings)-1].t < mk.end() {
		return fail(KindInsufficientReadings, op, "没有时刻不早于 %s 月末的读数", mk)
	}
	if a := m.months[mk]; a != nil && a.unpriceableSlices > 0 {
		return fail(KindUnpriceable, op, "月份 %s 存在不可计价片", mk)
	}
	m.seal(meter, mk)
	e.sealedMonths[mk]++
	if end := mk.end(); end > e.maxSealedMonthEnd {
		e.maxSealedMonthEnd = end
	}
	return nil
}

// Bill 查询某供电点某月账单。未封账月份反映当前最新状态；
// 已封账月份返回冻结快照。开销与已封账月份数量、其他供电点读数总量无关。
func (e *Engine) Bill(meter string, year, month int) (Bill, error) {
	const op = "Bill"
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := checkMeterParam(op, meter); err != nil {
		return Bill{}, err
	}
	mk, err := checkYearMonth(year, month)
	if err != nil {
		return Bill{}, fail(KindInvalidParam, op, "%v", err)
	}
	m := e.meters[meter]
	if m == nil {
		return materialize(meter, mk, nil, false), nil
	}
	return m.bill(meter, mk), nil
}
