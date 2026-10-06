package medschedule

import "sort"

// freqKind 是医嘱频次类型。
type freqKind int

const (
	freqInterval freqKind = iota // 固定间隔
	freqDaily                    // 固定时点
	freqPRN                      // 必要时
)

// order 是系统内医嘱的完整状态。
type order struct {
	id        string
	patient   string
	drug      string
	kind      freqKind
	createdAt int64
	stoppedAt int64 // -1 表示在执行
	h         int64 // 固定间隔：间隔
	firstTime int64 // 固定间隔：首次计划时刻

	timesOfDay []int64 // 固定时点：升序日内秒数
	prnMinGap  int64   // PRN：同医嘱最小间隔
	prnMax24   int64   // PRN：滚动 24h 次数上限

	// 固定间隔：补给重排的分段序列，按生成先后排列；仅最后一段可能未截断。
	// 截断段 i 的计划点从 seg[i+1].start 起全部作废。
	segs []intervalSeg
	// 已处理计划点：计划时刻 -> 记录类型。
	records map[int64]Status
	// PRN 实际给药时刻（升序）。
	prnDoses []int64
}

func (o *order) active() bool { return o.stoppedAt < 0 }

// Spec 描述新医嘱。Kind: "interval" | "daily" | "prn"。
type Spec struct {
	ID         string
	Patient    string
	Drug       string
	Kind       string
	FirstTime  int64   // interval
	H          int64   // interval
	TimesOfDay []int64 // daily：日内秒数集合
	PRNMinGap  int64   // prn
	PRNMax24   int64   // prn
}

// validateSpec 只做“参数非法”层面的校验（不查目录、过敏、时钟）。
func validateSpec(spec Spec, now int64) (*order, bool) {
	if !validNow(now) || !validateIDs(spec.ID, spec.Patient, spec.Drug) {
		return nil, false
	}
	o := &order{
		id:        spec.ID,
		patient:   spec.Patient,
		drug:      spec.Drug,
		createdAt: now,
		stoppedAt: -1,
		records:   map[int64]Status{},
	}
	switch spec.Kind {
	case "interval":
		if spec.H <= 0 || spec.FirstTime < now || spec.FirstTime > maxNow {
			return nil, false
		}
		o.kind = freqInterval
		o.h = spec.H
		o.firstTime = spec.FirstTime
		o.segs = []intervalSeg{{start: spec.FirstTime, cut: -1, madeUpAt: -1}}
	case "daily":
		if len(spec.TimesOfDay) == 0 {
			return nil, false
		}
		seen := map[int64]bool{}
		for _, t := range spec.TimesOfDay {
			if t < 0 || t >= dayLen || seen[t] {
				return nil, false
			}
			seen[t] = true
		}
		times := append([]int64(nil), spec.TimesOfDay...)
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		o.kind = freqDaily
		o.timesOfDay = times
	case "prn":
		if spec.PRNMinGap <= 0 || spec.PRNMax24 <= 0 {
			return nil, false
		}
		o.kind = freqPRN
		o.prnMinGap = spec.PRNMinGap
		o.prnMax24 = spec.PRNMax24
	default:
		return nil, false
	}
	return o, true
}

// validateSpacing 校验频次自身间隔：必须严格大于 2W 且不小于药品最小安全间隔。
func (s *System) validateSpacing(o *order, d *Drug) bool {
	minGap := d.MinIntervalSec
	switch o.kind {
	case freqInterval:
		return o.h > 2*s.w && o.h >= minGap
	case freqDaily:
		times := o.timesOfDay
		for i := 0; i < len(times); i++ {
			a := times[i]
			b := times[(i+1)%len(times)]
			gap := b - a
			if i == len(times)-1 {
				gap = b + dayLen - a
			}
			if gap <= 2*s.w || gap < minGap {
				return false
			}
		}
	}
	return true
}

// CreateOrder 开立医嘱。
func (s *System) CreateOrder(now int64, spec Spec) error {
	o, ok := validateSpec(spec, now)
	if !ok {
		return errInvalid("invalid order spec")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	d, exists := s.drugs[spec.Drug]
	if !exists {
		return errNotFound("drug not registered: " + spec.Drug)
	}
	if _, dup := s.orders[spec.ID]; dup {
		return errBadState("order id already exists: " + spec.ID)
	}
	if s.hasAllergy(spec.Patient, spec.Drug) {
		return errAllergy("patient is allergic to drug or category")
	}
	if !s.validateSpacing(o, d) {
		return errInvalid("frequency spacing violates 2W or drug minimum interval")
	}
	s.orders[spec.ID] = o
	s.commitClock(now)
	return nil
}

// StopOrder 停嘱，now 生效。
func (s *System) StopOrder(now int64, orderID string) error {
	if !validNow(now) || !validateIDs(orderID) {
		return errInvalid("invalid stop parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return errNotFound("order not found: " + orderID)
	}
	if !o.active() {
		return errBadState("order already stopped: " + orderID)
	}
	o.stoppedAt = now
	s.commitClock(now)
	return nil
}

// ReplaceOrder 改嘱：原子地停旧嘱并按 spec 开新嘱。
// 所有校验先于任何状态变更；任一失败则两步皆不生效、时钟也不推进。
func (s *System) ReplaceOrder(now int64, oldID string, spec Spec) error {
	o, ok := validateSpec(spec, now)
	if !ok || !validateIDs(oldID) || spec.ID == oldID {
		return errInvalid("invalid replace parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	old, exists := s.orders[oldID]
	if !exists {
		return errNotFound("order not found: " + oldID)
	}
	if !old.active() {
		return errBadState("order already stopped: " + oldID)
	}
	d, drugExists := s.drugs[spec.Drug]
	if !drugExists {
		return errNotFound("drug not registered: " + spec.Drug)
	}
	if _, dup := s.orders[spec.ID]; dup {
		return errBadState("order id already exists: " + spec.ID)
	}
	if s.hasAllergy(spec.Patient, spec.Drug) {
		return errAllergy("patient is allergic to drug or category")
	}
	if !s.validateSpacing(o, d) {
		return errInvalid("frequency spacing violates 2W or drug minimum interval")
	}
	// 全部校验通过后一并提交。
	old.stoppedAt = now
	s.orders[spec.ID] = o
	s.commitClock(now)
	return nil
}
