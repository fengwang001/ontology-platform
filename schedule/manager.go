package schedule

import (
	"sort"
	"sync"
)

// Manager 管理一组日程系列，所有方法可并发调用，
// 效果等价于某个串行顺序。
type Manager struct {
	mu     sync.Mutex
	series map[string]*series
}

func NewManager() *Manager {
	return &Manager{series: make(map[string]*series)}
}

// SeriesSpec 描述创建系列所需的全部输入。
// Count 与 Until 恰给一个：Count != 0 表示 count 型，Until != "" 表示 until 型。
type SeriesSpec struct {
	ID    string
	Start string
	Rule  Rule
	Count int
	Until string
}

func (m *Manager) CreateSeries(spec SeriesSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	start, err := ParseDate(spec.Start)
	if err != nil {
		return err
	}
	if err := spec.Rule.validate(); err != nil {
		return err
	}
	hasCount := spec.Count != 0
	hasUntil := spec.Until != ""
	if hasCount && spec.Count < 1 {
		return ErrInvalidCount
	}
	if hasCount == hasUntil {
		return ErrTermination
	}
	var until Date
	if hasUntil {
		until, err = ParseDate(spec.Until)
		if err != nil {
			return err
		}
		if less(until, start) {
			return ErrUntilBeforeStart
		}
	}
	if _, dup := m.series[spec.ID]; dup {
		return ErrSeriesExists
	}
	m.series[spec.ID] = &series{
		id:       spec.ID,
		start:    start,
		rule:     spec.Rule,
		hasCount: hasCount,
		count:    spec.Count,
		until:    until,
		exc:      make(map[Date]exception),
	}
	return nil
}

// getSeries 与 instanceDate 是各操作共用的校验步骤。
func (m *Manager) getSeries(id string) (*series, error) {
	s, ok := m.series[id]
	if !ok {
		return nil, ErrSeriesNotFound
	}
	return s, nil
}

func instanceDate(s *series, date string) (Date, error) {
	d, err := ParseDate(date)
	if err != nil {
		return Date{}, err
	}
	for _, o := range s.originals() {
		if o == d {
			return d, nil
		}
	}
	return Date{}, ErrNotInstanceDate
}

func (m *Manager) Cancel(seriesID, date string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.getSeries(seriesID)
	if err != nil {
		return err
	}
	d, err := instanceDate(s, date)
	if err != nil {
		return err
	}
	if e, ok := s.exc[d]; ok && e.canceled {
		return ErrAlreadyCanceled
	}
	// 对已改期的实例取消：覆盖例外即释放其改期日。
	s.exc[d] = exception{canceled: true}
	return nil
}

func (m *Manager) Reschedule(seriesID, date, newDate string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.getSeries(seriesID)
	if err != nil {
		return err
	}
	d, err := instanceDate(s, date)
	if err != nil {
		return err
	}
	target, err := ParseDate(newDate)
	if err != nil {
		return err
	}
	if e, ok := s.exc[d]; ok && e.canceled {
		return ErrAlreadyCanceled
	}
	// 占用检查：同系列其他未取消实例的实际日期（原位或改期而来）。
	for _, o := range s.originals() {
		if o == d {
			continue
		}
		if e, ok := s.exc[o]; ok {
			if e.canceled {
				continue
			}
			if e.movedTo == target {
				return ErrDateOccupied
			}
			continue
		}
		if o == target {
			return ErrDateOccupied
		}
	}
	// 覆盖旧例外即释放旧改期日。
	s.exc[d] = exception{movedTo: target}
	return nil
}

// Split 把 seriesID 在 date（实例原日期）处拆分：
// 原系列只保留 date 之前的实例，date 及之后的实例由新系列
// （newID + newRule，从 date 起算）承接剩余名额。
func (m *Manager) Split(seriesID, date, newID string, newRule Rule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.getSeries(seriesID)
	if err != nil {
		return err
	}
	d, err := instanceDate(s, date)
	if err != nil {
		return err
	}
	if err := newRule.validate(); err != nil {
		return err
	}
	if _, dup := m.series[newID]; dup {
		return ErrSeriesExists
	}

	orig := s.originals()
	before := 0
	for _, o := range orig {
		if less(o, d) {
			before++
		}
	}

	ns := &series{
		id:       newID,
		start:    d,
		rule:     newRule,
		hasCount: s.hasCount,
		until:    s.until,
		exc:      make(map[Date]exception),
	}
	if s.hasCount {
		ns.count = s.count - before
		s.count = before
	} else {
		s.until = d.addDays(-1)
	}
	// 丢弃原系列中属于 date 及之后实例的例外。
	for o := range s.exc {
		if !less(o, d) {
			delete(s.exc, o)
		}
	}
	m.series[newID] = ns
	return nil
}

// Expand 展开 [from, to) 内所有系列的实例，按 (实际日期, 系列 id, 原日期) 升序。
func (m *Manager) Expand(from, to string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := ParseDate(from)
	if err != nil {
		return nil, err
	}
	t, err := ParseDate(to)
	if err != nil {
		return nil, err
	}
	if !less(f, t) || t.dayCount()-f.dayCount() > 3660 {
		return nil, ErrInvalidRange
	}

	var out []Instance
	for _, s := range m.series {
		for _, inst := range s.expand() {
			if !less(inst.Actual, f) && less(inst.Actual, t) {
				out = append(out, inst)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Actual != b.Actual {
			return less(a.Actual, b.Actual)
		}
		if a.SeriesID != b.SeriesID {
			return a.SeriesID < b.SeriesID
		}
		return less(a.Original, b.Original)
	})
	return out, nil
}
