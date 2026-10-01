package schedule

import (
	"sort"
	"sync"
)

// Rule 描述“每隔 k 个月的第 nth 个星期 w”的重复规则。
// nth 取值 1..5 或 -1（该月最后一个）。
type Rule struct {
	K   int
	Nth int
	W   int
}

// CreateInput 是创建系列的输入。Count 与 Until 恰好给一个：
// 给 Count 时 Count>=1 且 Until 为空字符串；
// 给 Until 时 Until 为合法日期且 Count==0。
type CreateInput struct {
	ID    string
	Start string
	Rule  Rule
	Count int
	Until string
}

// RescheduleInput 是改期输入。
type RescheduleInput struct {
	ID      string
	Date    string
	NewDate string
}

// SplitInput 是“此次及以后”拆分输入。
type SplitInput struct {
	ID      string
	Date    string
	NewID   string
	NewRule Rule
}

// Instance 是展开结果中的一行。
type Instance struct {
	SeriesID string
	Original Date
	Actual   Date
}

// Series 是系列的对外快照。
type Series struct {
	ID       string
	Start    Date
	Rule     Rule
	Count    int
	Until    Date
	HasUntil bool
	Canceled map[string]bool
	Moved    map[string]Date
}

// Until 早于 1970（拆分 date 之前 0 实例）时 UntilOrd 仍可安全使用，
// 此时 UntilValid 为 false。
func (s Series) untilOrd() (int, bool) {
	if !s.HasUntil {
		return 0, false
	}
	return s.Until.ord, true
}

// Manager 是并发安全的系列管理器。
type Manager struct {
	mu     sync.RWMutex
	series map[string]*seriesState
}

// NewManager 创建空管理器。
func NewManager() *Manager { return &Manager{series: map[string]*seriesState{}} }

// Snapshot 返回全部系列在某一时刻的完整快照（id 字典序）。
func (m *Manager) Snapshot() []Series {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.series))
	for id := range m.series {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Series, 0, len(ids))
	for _, id := range ids {
		s := m.series[id]
		cp := Series{
			ID:       s.id,
			Start:    s.start,
			Rule:     s.rule,
			Count:    s.count,
			HasUntil: s.hasUntil,
			Canceled: map[string]bool{},
			Moved:    map[string]Date{},
		}
		if s.hasUntil {
			if d, ok := dateFromOrd(s.untilOrd); ok {
				cp.Until = d
			}
		}
		for k, v := range s.canceled {
			cp.Canceled[k] = v
		}
		for k, v := range s.moved {
			cp.Moved[k] = v
		}
		out = append(out, cp)
	}
	return out
}

type seriesState struct {
	id       string
	start    Date
	rule     Rule
	count    int
	untilOrd int
	hasUntil bool
	canceled map[string]bool
	moved    map[string]Date
}

func (s *seriesState) instances() []Date {
	return generateInstances(s.start, s.rule, s.count, s.untilOrd, s.hasUntil)
}

func (s *seriesState) findInstance(d Date) bool {
	for _, inst := range s.instances() {
		if inst.Equal(d) {
			return true
		}
	}
	return false
}

// actualDate 返回实例的实际日期（未改期即原日期）。
func (s *seriesState) actualDate(orig Date) Date {
	if a, ok := s.moved[orig.String()]; ok {
		return a
	}
	return orig
}

// occupied 报告 newDate 是否被同系列另一个未取消实例（原位或改期而来）占用。
func (s *seriesState) occupied(newDate Date, self string) bool {
	for _, inst := range s.instances() {
		key := inst.String()
		if s.canceled[key] || key == self {
			continue
		}
		if s.actualDate(inst).Equal(newDate) {
			return true
		}
	}
	return false
}

func cloneExceptions(s *seriesState) (map[string]bool, map[string]Date) {
	canceled := make(map[string]bool, len(s.canceled))
	for k, v := range s.canceled {
		canceled[k] = v
	}
	moved := make(map[string]Date, len(s.moved))
	for k, v := range s.moved {
		moved[k] = v
	}
	return canceled, moved
}

// Create 创建一个系列。任何参数非法都整体拒绝且不改动管理器。
func (m *Manager) Create(in CreateInput) error {
	if err := validateRule(in.Rule); err != nil {
		return err
	}
	start, err := ParseDate(in.Start)
	if err != nil {
		return err
	}
	hasCount := in.Count > 0
	hasUntil := in.Until != ""
	if hasCount == hasUntil {
		return opError(ErrInvalidEnd, "exactly one of count(>=1) or until must be given")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.series[in.ID]; exists {
		return opError(ErrDuplicateSeries, "series id already exists: "+in.ID)
	}

	st := &seriesState{
		id:       in.ID,
		start:    start,
		rule:     in.Rule,
		canceled: map[string]bool{},
		moved:    map[string]Date{},
	}
	if hasUntil {
		until, err := ParseDate(in.Until)
		if err != nil {
			return err
		}
		if until.Before(start) {
			return opError(ErrUntilBeforeStart, "until must not be before start")
		}
		st.hasUntil = true
		st.untilOrd = until.ord
	} else {
		if in.Count < 1 {
			return opError(ErrInvalidEnd, "count must be >= 1")
		}
		st.count = in.Count
	}
	m.series[in.ID] = st
	return nil
}

// Cancel 取消系列中原日期为 date 的实例；若该实例已改期，同时释放其改期日。
func (m *Manager) Cancel(id, date string) error {
	d, err := ParseDate(date)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.series[id]
	if !ok {
		return opError(ErrUnknownSeries, "unknown series id: "+id)
	}
	key := d.String()
	if !s.findInstance(d) {
		return opError(ErrNotInstance, date+" is not an instance of series "+id)
	}
	if s.canceled[key] {
		return opError(ErrAlreadyCanceled, "instance already canceled: "+date)
	}
	s.canceled[key] = true
	delete(s.moved, key)
	return nil
}

// Reschedule 把实例原日期 date 改期到 newDate。
// 对已改期实例再改期会释放旧改期日；已取消实例的改期按“取消已取消实例”同一原因拒绝。
func (m *Manager) Reschedule(in RescheduleInput) error {
	d, err := ParseDate(in.Date)
	if err != nil {
		return err
	}
	newDate, err := ParseDate(in.NewDate)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.series[in.ID]
	if !ok {
		return opError(ErrUnknownSeries, "unknown series id: "+in.ID)
	}
	key := d.String()
	if !s.findInstance(d) {
		return opError(ErrNotInstance, in.Date+" is not an instance of series "+in.ID)
	}
	if s.canceled[key] {
		return opError(ErrAlreadyCanceled, "instance is canceled: "+in.Date)
	}
	if s.occupied(newDate, key) {
		return opError(ErrDateOccupied, "target date already occupied in series "+in.ID+": "+in.NewDate)
	}
	s.moved[key] = newDate
	return nil
}

// Split 在 date 处执行“此次及以后”拆分。
// 原系列只保留 date 之前（按原日期）的实例与相关例外；新系列从 date 起按新规则展开。
func (m *Manager) Split(in SplitInput) (string, error) {
	d, err := ParseDate(in.Date)
	if err != nil {
		return "", err
	}
	if err := validateRule(in.NewRule); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.series[in.ID]
	if !ok {
		return "", opError(ErrUnknownSeries, "unknown series id: "+in.ID)
	}
	if _, dup := m.series[in.NewID]; dup {
		return "", opError(ErrDuplicateSeries, "new series id already exists: "+in.NewID)
	}
	if !s.findInstance(d) {
		return "", opError(ErrNotInstance, in.Date+" is not an instance of series "+in.ID)
	}

	instances := s.instances()
	before := instancesBefore(instances, d)

	old := &seriesState{
		id:       s.id,
		start:    s.start,
		rule:     s.rule,
		canceled: map[string]bool{},
		moved:    map[string]Date{},
	}
	if s.hasUntil {
		old.hasUntil = true
		old.untilOrd = d.ord - 1
	} else {
		old.count = before
	}
	for _, inst := range instances[:before] {
		key := inst.String()
		if s.canceled[key] {
			old.canceled[key] = true
		}
		if a, moved := s.moved[key]; moved {
			old.moved[key] = a
		}
	}

	ns := &seriesState{
		id:       in.NewID,
		start:    d,
		rule:     in.NewRule,
		canceled: map[string]bool{},
		moved:    map[string]Date{},
	}
	if s.hasUntil {
		ns.hasUntil = true
		ns.untilOrd = s.untilOrd
	} else {
		ns.count = s.count - before
	}

	m.series[s.id] = old
	m.series[ns.id] = ns
	return ns.id, nil
}

// Expand 展开 [from,to) 内所有系列的有效实例。
// 看到的是某一时刻全部系列的完整快照；取消的不出现；
// 改期实例按实际日期判定是否落入区间；结果按实际日期、系列 id、原日期升序。
func (m *Manager) Expand(from, to string) ([]Instance, error) {
	f, err := ParseDate(from)
	if err != nil {
		return nil, err
	}
	t, err := ParseDate(to)
	if err != nil {
		return nil, err
	}
	if !f.Before(t) {
		return nil, opError(ErrInvalidRange, "range must be non-empty: "+from+" .. "+to)
	}
	if t.ord-f.ord > 3660 {
		return nil, opError(ErrInvalidRange, "range span must not exceed 3660 days")
	}

	m.mu.RLock()
	snap := make([]*seriesState, 0, len(m.series))
	for _, s := range m.series {
		cp := &seriesState{
			id:       s.id,
			start:    s.start,
			rule:     s.rule,
			count:    s.count,
			untilOrd: s.untilOrd,
			hasUntil: s.hasUntil,
			canceled: make(map[string]bool, len(s.canceled)),
			moved:    make(map[string]Date, len(s.moved)),
		}
		for k, v := range s.canceled {
			cp.canceled[k] = v
		}
		for k, v := range s.moved {
			cp.moved[k] = v
		}
		snap = append(snap, cp)
	}
	m.mu.RUnlock()

	var out []Instance
	for _, s := range snap {
		for _, orig := range s.instances() {
			key := orig.String()
			if s.canceled[key] {
				continue
			}
			actual := orig
			if a, ok := s.moved[key]; ok {
				actual = a
			}
			if f.Before(actual) && actual.Before(t) {
				out = append(out, Instance{SeriesID: s.id, Original: orig, Actual: actual})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Actual.Equal(out[j].Actual) {
			return out[i].Actual.Before(out[j].Actual)
		}
		if out[i].SeriesID != out[j].SeriesID {
			return out[i].SeriesID < out[j].SeriesID
		}
		return out[i].Original.Before(out[j].Original)
	})
	return out, nil
}
