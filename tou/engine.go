package tou

import (
	"sort"
	"sync"
	"time"
)

// Engine 是分时电价结算引擎。所有方法并发安全：
// 一把 RWMutex 串行化写操作、并行化账单查询，结果等价于某串行顺序。
type Engine struct {
	loc     *time.Location
	mu      sync.RWMutex
	tariffs tariffBook
	cal     *calendar
	points  map[string]*pointState
}

type aggBucket struct {
	wh     int64
	amount int64
}

type monthState struct {
	agg        map[aggKey]*aggBucket
	unbillable int64
	totalWh    int64
	snapshot   *Bill // 非 nil 表示已封账冻结
}

type pointState struct {
	readings []Reading
	byAt     map[time.Time]int
	// gapSlices[i] 为区间 (readings[i], readings[i+1]) 的当前切片。
	gapSlices [][]slice
	months    map[monthKey]*monthState
	maxClosed monthKey
	hasClosed bool
}

func newPointState() *pointState {
	return &pointState{byAt: map[time.Time]int{}, months: map[monthKey]*monthState{}}
}

func (p *pointState) monthAt(k monthKey) *monthState {
	m := p.months[k]
	if m == nil {
		m = &monthState{agg: map[aggKey]*aggBucket{}}
		p.months[k] = m
	}
	return m
}

// New 创建引擎。loc 为 nil 时使用固定 UTC+8（不依赖系统 tzdata，
// 保证任何环境下日界与月末判定完全一致）。
func New(loc *time.Location) *Engine {
	if loc == nil {
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	return &Engine{loc: loc, cal: newCalendar(), points: map[string]*pointState{}}
}

// inLoc 把外部传入时刻按同一瞬间归一化到引擎时区，
// 避免不同 Location 表示同一瞬间导致月界/日界不一致。
func (e *Engine) inLoc(t time.Time) time.Time {
	return t.In(e.loc)
}

// RegisterTariff 登记电价表版本（全局）。生效时刻落在任何已封账月份
// 之内或之前时报 ErrMonthClosed；重复生效时刻报 ErrInvalidParameter。
// 成功后所有受影响的未封账月账单增量重算。
func (e *Engine) RegisterTariff(v TariffVersion) error {
	v.EffectiveAt = e.inLoc(v.EffectiveAt)
	if !validateVersion(v) {
		return ErrInvalidParameter
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.points {
		if p.hasClosed && !v.EffectiveAt.After(p.maxClosed.end(e.loc)) {
			return ErrMonthClosed
		}
	}
	if !e.tariffs.add(v) {
		return ErrInvalidParameter
	}
	for _, p := range e.points {
		e.resliceAfter(p, v.EffectiveAt)
	}
	return nil
}

// SetHoliday 登记或撤销某日期的节假日标记。日期格式 "YYYY-MM-DD"。
func (e *Engine) SetHoliday(date string, on bool) error {
	day, ok := ParseDate(date, e.loc)
	if !ok {
		return ErrInvalidParameter
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.points {
		if p.hasClosed && day.Before(p.maxClosed.end(e.loc)) {
			return ErrMonthClosed
		}
	}
	e.cal.setHoliday(day, on)
	dayEnd := nextDayStart(day)
	for _, p := range e.points {
		e.resliceRange(p, day, dayEnd)
	}
	return nil
}

// RegisterReading 追加一条读数（时刻必须晚于该点最新读数）。
func (e *Engine) RegisterReading(point string, at time.Time, wh int64) error {
	at = e.inLoc(at)
	if point == "" || !validMoment(at) || !validEnergy(wh) {
		return ErrInvalidParameter
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.points[point]
	if p == nil {
		p = newPointState()
		e.points[point] = p
	}
	if p.hasClosed && !at.After(p.maxClosed.end(e.loc)) {
		return ErrMonthClosed
	}
	if n := len(p.readings); n > 0 && !at.After(p.readings[n-1].At) {
		return ErrOutOfOrder
	}
	if n := len(p.readings); n > 0 && wh < p.readings[n-1].Wh {
		return ErrReadingRegression
	}
	e.appendReadingLocked(point, p, Reading{At: at, Wh: wh})
	return nil
}

// CorrectReading 用新累计电量替换已有读数（读数时刻不变）。
// 边界读数（恰为已封账月末的锚点读数）同样不可修正。
func (e *Engine) CorrectReading(point string, at time.Time, newWh int64) error {
	at = e.inLoc(at)
	if point == "" || !validMoment(at) || !validEnergy(newWh) {
		return ErrInvalidParameter
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.points[point]
	if p == nil {
		return ErrInvalidParameter
	}
	if p.hasClosed && !at.After(p.maxClosed.end(e.loc)) {
		return ErrMonthClosed
	}
	idx, ok := p.byAt[at]
	if !ok {
		return ErrInvalidParameter
	}
	if idx > 0 && newWh < p.readings[idx-1].Wh {
		return ErrReadingRegression
	}
	if idx+1 < len(p.readings) && newWh > p.readings[idx+1].Wh {
		return ErrReadingRegression
	}
	p.readings[idx].Wh = newWh
	idxs := make([]int, 0, 2)
	if idx > 0 {
		idxs = append(idxs, idx-1)
	}
	if idx+1 < len(p.readings) {
		idxs = append(idxs, idx)
	}
	e.rebuildGaps(p, idxs)
	return nil
}

// DeleteReading 删除最新一条读数（仅最新可删，供登记错误时回退）。
func (e *Engine) DeleteReading(point string, at time.Time) error {
	at = e.inLoc(at)
	if point == "" || !validMoment(at) {
		return ErrInvalidParameter
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.points[point]
	if p == nil {
		return ErrInvalidParameter
	}
	if p.hasClosed && !at.After(p.maxClosed.end(e.loc)) {
		return ErrMonthClosed
	}
	n := len(p.readings)
	if n == 0 || !p.readings[n-1].At.Equal(at) {
		return ErrOutOfOrder
	}
	if n >= 2 {
		e.removeGapContributions(p, n-2)
	}
	delete(p.byAt, at)
	p.readings = p.readings[:n-1]
	p.gapSlices = p.gapSlices[:max0(n-2)]
	return nil
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

func (e *Engine) appendReadingLocked(point string, p *pointState, r Reading) {
	idx := len(p.readings)
	p.readings = append(p.readings, r)
	p.byAt[r.At] = idx
	if idx > 0 {
		ss := sliceInterval(p.readings[idx-1].At, r.At,
			r.Wh-p.readings[idx-1].Wh, &e.tariffs, e.cal)
		p.gapSlices = append(p.gapSlices, ss)
		e.addGapContributions(p, ss)
	}
}

func (e *Engine) rebuildGap(p *pointState, idx int) {
	if idx < 0 || idx+1 >= len(p.readings) {
		return
	}
	e.removeGapContributions(p, idx)
	ss := sliceInterval(p.readings[idx].At, p.readings[idx+1].At,
		p.readings[idx+1].Wh-p.readings[idx].Wh, &e.tariffs, e.cal)
	p.gapSlices[idx] = ss
	e.addGapContributions(p, ss)
}

func (e *Engine) rebuildGaps(p *pointState, idxs []int) {
	seen := map[int]bool{}
	uniq := idxs[:0]
	for _, i := range idxs {
		if i >= 0 && i+1 < len(p.readings) && !seen[i] {
			seen[i] = true
			uniq = append(uniq, i)
		}
	}
	sort.Ints(uniq)
	for _, i := range uniq {
		e.removeGapContributions(p, i)
	}
	for _, i := range uniq {
		ss := sliceInterval(p.readings[i].At, p.readings[i+1].At,
			p.readings[i+1].Wh-p.readings[i].Wh, &e.tariffs, e.cal)
		p.gapSlices[i] = ss
		e.addGapContributions(p, ss)
	}
}

// resliceAfter 重切所有与 [from, +∞) 相交的区间（版本回溯登记）。
func (e *Engine) resliceAfter(p *pointState, from time.Time) {
	if len(p.readings) < 2 {
		return
	}
	first := sort.Search(len(p.readings)-1, func(i int) bool {
		return p.readings[i+1].At.After(from)
	})
	idxs := make([]int, 0, len(p.readings)-1-first)
	for i := first; i < len(p.readings)-1; i++ {
		idxs = append(idxs, i)
	}
	e.rebuildGaps(p, idxs)
}

// resliceRange 重切所有与 [from, to) 相交的区间（节假日变更）。
func (e *Engine) resliceRange(p *pointState, from, to time.Time) {
	if len(p.readings) < 2 {
		return
	}
	lo := sort.Search(len(p.readings)-1, func(i int) bool {
		return p.readings[i+1].At.After(from)
	})
	// gap i 与 [from,to) 相交当且仅当 r[i+1] > from 且 r[i] < to。
	hi := len(p.readings) - 1
	for hi > 0 && !p.readings[hi-1].At.Before(to) {
		hi--
	}
	idxs := make([]int, 0, hi-lo)
	for i := lo; i < hi; i++ {
		idxs = append(idxs, i)
	}
	e.rebuildGaps(p, idxs)
}

func (e *Engine) addGapContributions(p *pointState, ss []slice) {
	for _, s := range ss {
		m := p.monthAt(s.month)
		if m.snapshot != nil {
			continue // 封账月冻结，防御性跳过
		}
		m.totalWh += s.energyWh
		if !s.billable {
			m.unbillable += s.energyWh
			continue
		}
		k := aggKey{month: s.month, day: s.day, startSec: s.startSec, price: s.price, billable: true}
		b := m.agg[k]
		if b == nil {
			b = &aggBucket{}
			m.agg[k] = b
		}
		b.wh += s.energyWh
		b.amount += s.amountMilli
	}
}

func (e *Engine) removeGapContributions(p *pointState, idx int) {
	if idx < 0 || idx >= len(p.gapSlices) {
		return
	}
	for _, s := range p.gapSlices[idx] {
		m := p.months[s.month]
		if m == nil || m.snapshot != nil {
			continue
		}
		m.totalWh -= s.energyWh
		if !s.billable {
			m.unbillable -= s.energyWh
			continue
		}
		k := aggKey{month: s.month, day: s.day, startSec: s.startSec, price: s.price, billable: true}
		b := m.agg[k]
		if b != nil {
			b.wh -= s.energyWh
			b.amount -= s.amountMilli
			if b.wh == 0 && b.amount == 0 {
				delete(m.agg, k)
			}
		}
	}
}

// Bill 查询某供电点某月账单，开销与已封账月数量及其他供电点无关。
func (e *Engine) Bill(point string, month time.Time) (Bill, error) {
	month = e.inLoc(month)
	if point == "" || !validMoment(month) {
		return Bill{}, ErrInvalidParameter
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	k := monthKeyAt(month)
	p := e.points[point]
	if p == nil {
		return Bill{Point: point, MonthStart: k.start(e.loc)}, nil
	}
	if m := p.months[k]; m != nil && m.snapshot != nil {
		return *m.snapshot, nil
	}
	return e.buildBill(point, p, k), nil
}

func (e *Engine) buildBill(point string, p *pointState, k monthKey) Bill {
	b := Bill{Point: point, MonthStart: k.start(e.loc)}
	m := p.months[k]
	if m == nil {
		return b
	}
	b.TotalWh = m.totalWh
	b.UnbillableWh = m.unbillable
	for key, v := range m.agg {
		if !key.billable || (v.wh == 0 && v.amount == 0) {
			continue
		}
		b.Entries = append(b.Entries, BillEntry{
			Day: key.day, StartSec: key.startSec, Price: key.price,
			EnergyWh: v.wh, AmountMilli: v.amount,
		})
		b.TotalAmountMilli += v.amount
	}
	sort.Slice(b.Entries, func(i, j int) bool {
		if b.Entries[i].Day != b.Entries[j].Day {
			return b.Entries[i].Day < b.Entries[j].Day
		}
		if b.Entries[i].StartSec != b.Entries[j].StartSec {
			return b.Entries[i].StartSec < b.Entries[j].StartSec
		}
		return b.Entries[i].Price < b.Entries[j].Price
	})
	return b
}

// CloseMonth 封账。先查不可计价片（ErrUnbillable），再查月末读数
// （ErrInsufficientReadings）。成功后冻结账单并拒绝一切影响该月的操作。
func (e *Engine) CloseMonth(point string, month time.Time) error {
	month = e.inLoc(month)
	if point == "" || !validMoment(month) {
		return ErrInvalidParameter
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	k := monthKeyAt(month)
	p := e.points[point]
	if p == nil {
		return ErrInsufficientReadings
	}
	if m := p.months[k]; m != nil && m.snapshot != nil {
		return nil
	}
	m := p.monthAt(k)
	if m.unbillable > 0 {
		return ErrUnbillable
	}
	end := k.end(e.loc)
	if n := len(p.readings); n == 0 || p.readings[n-1].At.Before(end) {
		return ErrInsufficientReadings
	}
	b := e.buildBill(point, p, k)
	b.Closed = true
	m.snapshot = &b
	if !p.hasClosed || k > p.maxClosed {
		p.maxClosed = k
		p.hasClosed = true
	}
	return nil
}
