package metering

import (
	"sort"
	"sync"
)

type periodKey struct{ start, end int64 }

// Service 为楼宇抄表记账与公摊分摊服务。所有公开方法可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Service struct {
	mu          sync.Mutex
	gap         int64
	now         int64
	hasNow      bool
	master      *meter
	units       []*meter // 按户号升序
	byID        map[string]*meter
	bills       map[periodKey]*Bill
	current     map[periodKey]*Bill // 各账期最近一次重算视图（更正以此为基准取增量）
	corrections []Correction
	stats       Stats
}

// New 创建服务，gap 为估抄准入门槛（距上一次实抄须严格超过 gap）。
func New(cfg Config) *Service {
	return &Service{
		gap:     cfg.Gap,
		byID:    make(map[string]*meter),
		bills:   make(map[periodKey]*Bill),
		current: make(map[periodKey]*Bill),
	}
}

// checkClock 校验时钟不回退。
func (s *Service) checkClock(now int64) *Error {
	if s.hasNow && now < s.now {
		return fail(ErrClockRollback, "now=%d 小于上一次被接受操作的 now=%d", now, s.now)
	}
	return nil
}

func (s *Service) advance(now int64) {
	s.now = now
	s.hasNow = true
}

// AddMeter 登记一只表。整栋楼只允许一只总表；分户表须带正建筑面积。
func (s *Service) AddMeter(now int64, id string, role Role, rangeMax, area int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || rangeMax <= 0 || (role != Master && role != Unit) ||
		(role == Master && area != 0) || (role == Unit && area <= 0) {
		return fail(ErrInvalidParam, "表参数非法: id=%q role=%d rangeMax=%d area=%d", id, role, rangeMax, area)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, dup := s.byID[id]; dup {
		return fail(ErrInvalidParam, "表 %s 已存在", id)
	}
	if role == Master && s.master != nil {
		return fail(ErrInvalidParam, "总表已存在")
	}
	m := &meter{id: id, role: role, rangeMax: rangeMax, area: area}
	s.byID[id] = m
	if role == Master {
		s.master = m
	} else {
		s.units = append(s.units, m)
		sort.Slice(s.units, func(i, j int) bool { return s.units[i].id < s.units[j].id })
	}
	s.advance(now)
	return nil
}

// AddOccupancy 为分户登记一段在住记录（左闭右开）。
func (s *Service) AddOccupancy(now int64, unitID string, start, end int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start < 0 || start >= end {
		return fail(ErrInvalidParam, "在住区间非法: [%d,%d)", start, end)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	m, ok := s.byID[unitID]
	if !ok || m.role != Unit {
		return fail(ErrMeterNotFound, "分户表 %s 不存在", unitID)
	}
	m.occupancy = mergeIntervals(append(m.occupancy, interval{start, end}))
	s.advance(now)
	return nil
}

// mergeIntervals 归并区间，避免重叠区间重复计算在住时长。
func mergeIntervals(ivs []interval) []interval {
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].start < ivs[j].start })
	out := ivs[:0]
	for _, iv := range ivs {
		if n := len(out); n > 0 && iv.start <= out[n-1].end {
			out[n-1].end = max(out[n-1].end, iv.end)
			continue
		}
		out = append(out, iv)
	}
	return out
}

// Record 录入一条读数。实抄晚于估抄时替代此前全部连续估抄；
// 影响已结算账期时返回链式更正。
func (s *Service) Record(now int64, meterID string, t, v int64, kind Kind) ([]Correction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if meterID == "" || t < 0 || v < 0 || t > now || (kind != Actual && kind != Estimate) {
		return nil, fail(ErrInvalidParam, "读数参数非法: meter=%q t=%d v=%d kind=%d now=%d", meterID, t, v, kind, now)
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	m, ok := s.byID[meterID]
	if !ok {
		return nil, fail(ErrMeterNotFound, "表 %s 不存在", meterID)
	}
	if v > m.rangeMax {
		return nil, fail(ErrInvalidParam, "读数 %d 超出量程上限 %d", v, m.rangeMax)
	}
	affected, err := m.appendReading(t, v, kind == Estimate, s.gap)
	if err != nil {
		return nil, err
	}
	s.advance(now)
	return s.recomputeCorrections(affected), nil
}

// ReplaceMeter 换表：旧表按 oldFinal 结算，新表自 newStart 起算，当刻不计用量。
func (s *Service) ReplaceMeter(now int64, meterID string, t, oldFinal, newStart int64) ([]Correction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if meterID == "" || t < 0 || oldFinal < 0 || newStart < 0 || t > now {
		return nil, fail(ErrInvalidParam, "换表参数非法: meter=%q t=%d old=%d new=%d", meterID, t, oldFinal, newStart)
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	m, ok := s.byID[meterID]
	if !ok {
		return nil, fail(ErrMeterNotFound, "表 %s 不存在", meterID)
	}
	if oldFinal > m.rangeMax || newStart > m.rangeMax {
		return nil, fail(ErrInvalidParam, "换表读数超出量程上限 %d", m.rangeMax)
	}
	affected, err := m.appendReplacement(t, oldFinal, newStart)
	if err != nil {
		return nil, err
	}
	s.advance(now)
	return s.recomputeCorrections(affected), nil
}

// Settle 结算账期 [start, end)。公摊为负或重复结算时拒绝且不留痕。
func (s *Service) Settle(now int64, start, end int64) (*Bill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start < 0 || start >= end || end > now || s.master == nil {
		return nil, fail(ErrInvalidParam, "账期参数非法: [%d,%d) now=%d", start, end, now)
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	key := periodKey{start, end}
	if _, dup := s.bills[key]; dup {
		return nil, fail(ErrPeriodSettled, "账期 [%d,%d) 已结算", start, end)
	}
	bill, err := s.computeBill(start, end, false)
	if err != nil {
		return nil, err
	}
	s.bills[key] = bill
	s.current[key] = bill
	s.advance(now)
	return bill, nil
}

// Bill 查询已结算账期的账单。
func (s *Service) Bill(start, end int64) (*Bill, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bills[periodKey{start, end}]
	return b, ok
}

// Corrections 返回迄今生成的全部更正（按生成顺序）。
func (s *Service) Corrections() []Correction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Correction(nil), s.corrections...)
}

// Stats 返回性能计数快照，用于验证结算开销与历史读数总量无关。
func (s *Service) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}
