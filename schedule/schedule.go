package schedule

import (
	"sort"
	"sync"
)

// exception 记录单个实例相对原系列的状态。
// cancelled=true 表示取消；reschedule 非零表示改期后的实际日期序号。
type exception struct {
	cancelled  bool
	reschedule int
}

// series 是一个“按月第 N 个星期几”重复的日程系列。
type series struct {
	id      string
	start   int
	rule    Rule
	isCount bool
	count   int
	until   int
	ex      map[int]*exception
}

// Instance 是展开结果中的一个实例。
type Instance struct {
	SeriesID string
	Original string
	Actual   string
}

// Manager 管理若干日程系列，所有方法可并发调用，
// 结果等价于某个串行顺序；展开看到的是某一时刻的完整快照。
type Manager struct {
	mu     sync.RWMutex
	series map[string]*series
	logger Logger
}

// Logger 用于打印输入、输出与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// NewManager 创建管理器，logger 为 nil 时丢弃日志。
func NewManager(logger Logger) *Manager {
	return &Manager{series: map[string]*series{}, logger: logger}
}

func (m *Manager) log(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}

// Create 创建一个系列。
func (m *Manager) Create(in CreateInput) error {
	start, err := parseDate(in.Start)
	if err != nil {
		m.log("CREATE REJECT input=%+v reason=%v", in, err)
		return err
	}
	if err := validateRule(in.Rule); err != nil {
		m.log("CREATE REJECT input=%+v reason=%v", in, err)
		return err
	}
	isCount, count, until, err := parseTerm(in.Term, start)
	if err != nil {
		m.log("CREATE REJECT input=%+v reason=%v", in, err)
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.series[in.ID]; ok {
		err := errf(ErrDuplicateID, "series id %q already exists", in.ID)
		m.log("CREATE REJECT input=%+v reason=%v", in, err)
		return err
	}
	s := &series{
		id:      in.ID,
		start:   start,
		rule:    in.Rule,
		isCount: isCount,
		count:   count,
		until:   until,
		ex:      map[int]*exception{},
	}
	m.series[in.ID] = s
	orig := s.originalDates()
	m.log("CREATE OK id=%q start=%s rule={k:%d nth:%d w:%d} term=%s original_dates=%v total=%d",
		in.ID, in.Start, in.Rule.IntervalMonths, in.Rule.Nth, in.Rule.Weekday,
		termDesc(s), ordinalStrings(orig), len(orig))
	return nil
}

// Cancel 取消某系列在 date（实例原日期）上的实例。
func (m *Manager) Cancel(id, date string) error {
	day, err := parseDate(date)
	if err != nil {
		m.log("CANCEL REJECT id=%q date=%q reason=%v", id, date, err)
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.requireSeries(id)
	if err != nil {
		m.log("CANCEL REJECT id=%q date=%s reason=%v", id, date, err)
		return err
	}
	if err := s.requireInstance(day); err != nil {
		m.log("CANCEL REJECT id=%q date=%s reason=%v", id, date, err)
		return err
	}
	if x := s.ex[day]; x != nil && x.cancelled {
		err := errf(ErrAlreadyCancelled, "instance %s of %q is already cancelled", date, id)
		m.log("CANCEL REJECT id=%q date=%s reason=%v", id, date, err)
		return err
	}
	// 对已改期实例取消：覆盖记录，旧改期日随之释放。
	s.ex[day] = &exception{cancelled: true}
	m.log("CANCEL OK id=%q date=%s judgement=hidden_and_actual_date_released", id, date)
	return nil
}

// Reschedule 将某系列 date 上的实例改期到 target（实际日期可为任意公历日，不必满足规则）。
func (m *Manager) Reschedule(id, date, target string) error {
	day, err := parseDate(date)
	if err != nil {
		m.log("RESCHEDULE REJECT id=%q date=%q target=%q reason=%v", id, date, target, err)
		return err
	}
	tgt, err := parseDate(target)
	if err != nil {
		m.log("RESCHEDULE REJECT id=%q date=%s target=%q reason=%v", id, date, target, err)
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.requireSeries(id)
	if err != nil {
		m.log("RESCHEDULE REJECT id=%q date=%s target=%s reason=%v", id, date, target, err)
		return err
	}
	if err := s.requireInstance(day); err != nil {
		m.log("RESCHEDULE REJECT id=%q date=%s target=%s reason=%v", id, date, target, err)
		return err
	}
	x := s.ex[day]
	if x != nil && x.cancelled {
		err := errf(ErrAlreadyCancelled, "instance %s of %q is cancelled; rescheduling a cancelled instance is rejected", date, id)
		m.log("RESCHEDULE REJECT id=%q date=%s target=%s reason=%v", id, date, target, err)
		return err
	}
	for _, orig := range s.originalDates() {
		if orig == day {
			continue
		}
		other := s.ex[orig]
		if other != nil && other.cancelled {
			continue // 已取消实例不占任何日期
		}
		if s.actualOf(orig) == tgt {
			err := errf(ErrTargetOccupied, "target %s is already used by another instance (original %s) of %q", target, formatDate(orig), id)
			m.log("RESCHEDULE REJECT id=%q date=%s target=%s reason=%v", id, date, target, err)
			return err
		}
	}
	if x == nil {
		s.ex[day] = &exception{reschedule: tgt}
	} else {
		m.log("RESCHEDULE judgement id=%q old_actual=%s released new_actual=%s", id, formatDate(x.reschedule), target)
		x.reschedule = tgt
	}
	m.log("RESCHEDULE OK id=%q original=%s actual=%s", id, date, target)
	return nil
}

// SplitInput 是“此次及以后”拆分的入参。
type SplitInput struct {
	ID      string
	Date    string
	NewID   string
	NewRule Rule
}

// Split 在 date 处把系列拆为“date 之前”（留原系列）与“date 及以后”（新系列）。
func (m *Manager) Split(in SplitInput) error {
	day, err := parseDate(in.Date)
	if err != nil {
		m.log("SPLIT REJECT input=%+v reason=%v", in, err)
		return err
	}
	if err := validateRule(in.NewRule); err != nil {
		m.log("SPLIT REJECT input=%+v reason=%v", in, err)
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.requireSeries(in.ID)
	if err != nil {
		m.log("SPLIT REJECT input=%+v reason=%v", in, err)
		return err
	}
	if err := s.requireInstance(day); err != nil {
		m.log("SPLIT REJECT input=%+v reason=%v (must be an instance original date, cancelled/rescheduled included)", in, err)
		return err
	}
	if _, ok := m.series[in.NewID]; ok {
		err := errf(ErrDuplicateID, "series id %q already exists", in.NewID)
		m.log("SPLIT REJECT input=%+v reason=%v", in, err)
		return err
	}

	before := 0
	for _, orig := range s.originalDates() {
		if orig < day {
			before++
		}
	}

	// 丢弃属于 date 及之后实例的例外；原系列收缩为 date 之前。
	for orig := range s.ex {
		if orig >= day {
			delete(s.ex, orig)
		}
	}
	wasCount := s.isCount
	oldCount := s.count
	oldUntil := s.until
	if wasCount {
		s.count = before
	} else {
		s.until = day - 1
	}

	ns := &series{
		id:    in.NewID,
		start: day,
		rule:  in.NewRule,
		ex:    map[int]*exception{},
	}
	if wasCount {
		ns.isCount = true
		ns.count = oldCount - before
	} else {
		ns.until = oldUntil
	}

	newOrig := ns.originalDates()
	m.series[in.NewID] = ns
	m.log("SPLIT OK old_id=%q at=%s instances_before=%d old_term_now=%s new_id=%q new_rule={k:%d nth:%d w:%d} new_term=%s new_originals=%v",
		in.ID, in.Date, before, termDesc(s), in.NewID,
		in.NewRule.IntervalMonths, in.NewRule.Nth, in.NewRule.Weekday, termDesc(ns), ordinalStrings(newOrig))
	return nil
}

// Expand 展开 [from,to) 内所有系列的实例，按 (实际日期, 系列 id, 原日期) 升序返回。
func (m *Manager) Expand(from, to string) ([]Instance, error) {
	f, err := parseDate(from)
	if err != nil {
		m.log("EXPAND REJECT from=%q to=%q reason=%v", from, to, err)
		return nil, err
	}
	t, err := parseDate(to)
	if err != nil {
		m.log("EXPAND REJECT from=%s to=%q reason=%v", from, to, err)
		return nil, err
	}
	if f >= t {
		err := errf(ErrEmptyRange, "[%s,%s) is empty", from, to)
		m.log("EXPAND REJECT from=%s to=%s reason=%v", from, to, err)
		return nil, err
	}
	if t-f > 3660 {
		err := errf(ErrRangeTooWide, "span %d days exceeds 3660", t-f)
		m.log("EXPAND REJECT from=%s to=%s reason=%v", from, to, err)
		return nil, err
	}

	// 持读锁克隆全部系列，保证看到的是某一时刻的完整快照。
	m.mu.RLock()
	snapshot := make([]*series, 0, len(m.series))
	for _, s := range m.series {
		snapshot = append(snapshot, s.clone())
	}
	m.mu.RUnlock()

	var out []Instance
	for _, s := range snapshot {
		for _, orig := range s.originalDates() {
			x := s.ex[orig]
			if x != nil && x.cancelled {
				m.log("EXPAND skip id=%q original=%s judgement=cancelled", s.id, formatDate(orig))
				continue
			}
			actual := s.actualOf(orig)
			if actual < f || actual >= t {
				m.log("EXPAND skip id=%q original=%s actual=%s judgement=actual_out_of_range", s.id, formatDate(orig), formatDate(actual))
				continue
			}
			out = append(out, Instance{
				SeriesID: s.id,
				Original: formatDate(orig),
				Actual:   formatDate(actual),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Actual != out[j].Actual {
			return out[i].Actual < out[j].Actual
		}
		if out[i].SeriesID != out[j].SeriesID {
			return out[i].SeriesID < out[j].SeriesID
		}
		return out[i].Original < out[j].Original
	})
	m.log("EXPAND OK [%s,%s) count=%d instances=%v", from, to, len(out), out)
	return out, nil
}

// ---- 内部辅助 ----

func validateRule(r Rule) error {
	if r.IntervalMonths < 1 {
		return errf(ErrBadInterval, "k=%d must be >= 1", r.IntervalMonths)
	}
	if r.Nth < -1 || r.Nth == 0 || r.Nth > 5 {
		return errf(ErrBadNth, "nth=%d must be 1..5 or -1", r.Nth)
	}
	if r.Weekday < 1 || r.Weekday > 7 {
		return errf(ErrBadWeekday, "weekday=%d must be 1..7 (Mon=1)", r.Weekday)
	}
	return nil
}

func parseTerm(t Termination, start int) (isCount bool, count, until int, err error) {
	hasCount := t.Count != 0
	hasUntil := t.Until != ""
	if hasCount == hasUntil {
		return false, 0, 0, errf(ErrTerminalCountUntil, "exactly one of count or until must be given")
	}
	if hasCount {
		if t.Count < 1 {
			return false, 0, 0, errf(ErrCountRange, "count=%d must be >= 1", t.Count)
		}
		return true, t.Count, 0, nil
	}
	until, err = parseDate(t.Until)
	if err != nil {
		return false, 0, 0, err
	}
	if until < start {
		return false, 0, 0, errf(ErrUntilBeforeStart, "until %s is before start", t.Until)
	}
	return false, 0, until, nil
}

func (m *Manager) requireSeries(id string) (*series, error) {
	s, ok := m.series[id]
	if !ok {
		return nil, errf(ErrUnknownSeries, "series id %q does not exist", id)
	}
	return s, nil
}

// requireInstance 判定 day 是否为系列的实例原日期（取消/改期不改变成员资格）。
func (s *series) requireInstance(day int) error {
	for _, orig := range s.originalDates() {
		if orig == day {
			return nil
		}
	}
	return errf(ErrNotInstance, "%s is not an instance original date of series %q", formatDate(day), s.id)
}

// actualOf 返回实例的实际日期序号（未改期即在原位）。
func (s *series) actualOf(orig int) int {
	if x := s.ex[orig]; x != nil && !x.cancelled && x.reschedule != 0 {
		return x.reschedule
	}
	return orig
}

// clone 深拷贝系列，供展开快照使用。
func (s *series) clone() *series {
	cp := *s
	cp.ex = make(map[int]*exception, len(s.ex))
	for k, v := range s.ex {
		ev := *v
		cp.ex[k] = &ev
	}
	return &cp
}

// originalDates 按规则生成全部实例原日期。
// start 所在月为第 0 个月，之后每隔 k 个月取该月第 nth 个星期 w 作为候选：
// 早于 start 的候选不算；第 5 个星期不存在的月份跳过且不占名额；
// 候选晚于 2200-12-31 不再生成；count/until 均按原日期计。
// 取消与改期不改变成员资格，count 照算。
func (s *series) originalDates() []int {
	if s.isCount && s.count == 0 {
		// “date 之前实例数为 0”的拆分残余系列：系列保留但无实例。
		return nil
	}
	maxOrd := ordinalOf(maxYear, 12, 31)
	sy, sm, _ := ymd(s.start)
	out := make([]int, 0)
	for i := 0; ; i++ {
		y, mo := addMonths(sy, sm, i*s.rule.IntervalMonths)
		if y > maxYear {
			break
		}
		cand, ok := nthWeekdayOfMonth(y, mo, s.rule.Nth, s.rule.Weekday)
		if !ok {
			// 该月没有第 5 个星期 w：跳过，不顺延、不占名额。
			continue
		}
		if cand < s.start {
			// 同月候选早于 start（或其它早于 start 的情况）：不算实例。
			continue
		}
		if cand > maxOrd {
			break
		}
		if !s.isCount && cand > s.until {
			// 月份单调推进，后续候选只会更晚。
			break
		}
		out = append(out, cand)
		if s.isCount && len(out) >= s.count {
			break
		}
	}
	return out
}

func termDesc(s *series) string {
	if s.isCount {
		return "count=" + itoa(s.count)
	}
	return "until=" + formatDate(s.until)
}

func ordinalStrings(ords []int) []string {
	out := make([]string, len(ords))
	for i, o := range ords {
		out[i] = formatDate(o)
	}
	return out
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
