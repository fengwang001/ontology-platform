package slots

import (
	"io"
	"sort"
	"time"
)

// naiveSeries 是对照模型自带的系列表示，与主模型结构刻意分开。
type naiveSeries struct {
	id         string
	airline    string
	day, hour  int
	start, end int
	returned   map[int]bool
	returnedAt map[int]time.Time
	register   map[int]RegStatus
}

type naiveApp struct {
	id                    string
	airline               string
	day, hour, start, end int
	at                    time.Time
}

// NaiveModel 按同一份规则独立实现：不维护任何占用/持有索引，
// 所有容量与持有判定都线性扫描全部系列，用于随机差分对照。
type NaiveModel struct {
	cfg      Config
	phase    phase
	last     time.Time
	hasClock bool
	airlines map[string]bool
	apps     map[string]*naiveApp
	hist     map[string]map[HistKey]bool
	series   map[string]*naiveSeries
	wait     map[slotKey][]*naiveApp
	appN     int
	serN     int
	quals    []Qualification
	log      io.Writer
}

func NewNaiveModel(cfg Config, hist map[string][]HistKey, log io.Writer) (*NaiveModel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	ha := map[string]map[HistKey]bool{}
	for al, keys := range hist {
		set := map[HistKey]bool{}
		for _, k := range keys {
			if !cfg.validRange(k.Day, k.Hour, k.StartWeek, k.EndWeek) {
				return nil, ErrInvalid
			}
			set[k] = true
		}
		ha[al] = set
	}
	return &NaiveModel{
		cfg: cfg, phase: phaseAccepting, airlines: map[string]bool{},
		apps: map[string]*naiveApp{}, hist: ha,
		series: map[string]*naiveSeries{}, wait: map[slotKey][]*naiveApp{}, log: log,
	}, nil
}

func (m *NaiveModel) advance(at time.Time) error {
	if m.hasClock && at.Before(m.last) {
		return ErrClock
	}
	return nil
}

func (m *NaiveModel) commit(at time.Time) { m.last, m.hasClock = at, true }

func (m *NaiveModel) RegisterAirline(at time.Time, id string) error {
	if id == "" {
		return ErrInvalid
	}
	if err := m.advance(at); err != nil {
		return err
	}
	if m.airlines[id] {
		return ErrInvalid
	}
	m.airlines[id] = true
	m.commit(at)
	return nil
}

func (m *NaiveModel) Apply(at time.Time, airline string, day, hour, sw, ew int) (string, error) {
	if !m.cfg.validRange(day, hour, sw, ew) || airline == "" {
		return "", ErrInvalid
	}
	if err := m.advance(at); err != nil {
		return "", err
	}
	if !m.airlines[airline] {
		return "", ErrNotFound
	}
	if m.phase != phaseAccepting || at.After(m.cfg.ApplyDeadline) {
		return "", ErrApplyClosed
	}
	m.appN++
	id := appID(m.appN)
	m.apps[id] = &naiveApp{id, airline, day, hour, sw, ew, at}
	m.commit(at)
	return id, nil
}

func (m *NaiveModel) sortedApps() []*naiveApp {
	out := make([]*naiveApp, 0, len(m.apps))
	for _, a := range m.apps {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.Before(out[j].at)
		}
		return out[i].id < out[j].id
	})
	return out
}

// count 线性统计某单元格在给定系列集合下的已分配数。
func (m *NaiveModel) count(list []*naiveSeries, w, d, h int) int {
	n := 0
	for _, s := range list {
		if s.day == d && s.hour == h && w >= s.start && w <= s.end && !s.returned[w] {
			n++
		}
	}
	return n
}

func (m *NaiveModel) allSeries() []*naiveSeries {
	out := make([]*naiveSeries, 0, len(m.series))
	for _, s := range m.series {
		out = append(out, s)
	}
	return out
}

func (m *NaiveModel) held(al string) int {
	n := 0
	for _, s := range m.series {
		if s.airline == al {
			n++
		}
	}
	return n
}

func (m *NaiveModel) create(a *naiveApp) *naiveSeries {
	m.serN++
	s := &naiveSeries{
		id: seriesID(m.serN), airline: a.airline, day: a.day, hour: a.hour,
		start: a.start, end: a.end,
		returned: map[int]bool{}, returnedAt: map[int]time.Time{},
		register: map[int]RegStatus{},
	}
	m.series[s.id] = s
	return s
}

func (m *NaiveModel) SettleAllocation(at time.Time) error {
	if err := m.advance(at); err != nil {
		return err
	}
	if m.phase == phaseSettled {
		return ErrSeasonSettled
	}
	if m.phase == phaseAllocated || at.Before(m.cfg.ApplyDeadline) {
		return ErrApplyClosed
	}
	apps := m.sortedApps()
	done := map[string]bool{}

	var created []*naiveSeries
	var hist []*naiveApp
	for _, a := range apps {
		if set := m.hist[a.airline]; set != nil &&
			set[HistKey{a.day, a.hour, a.start, a.end}] {
			hist = append(hist, a)
		}
	}
	for _, a := range hist {
		for w := a.start; w <= a.end; w++ {
			if m.count(created, w, a.day, a.hour) >= m.cfg.Capacity[a.day][a.hour] {
				return ErrInvalid
			}
		}
		created = append(created, m.create(a))
		done[a.id] = true
	}

	base := map[cellKey]int{}
	for _, s := range created {
		for w := s.start; w <= s.end; w++ {
			base[cellKey{w, s.day, s.hour}]++
		}
	}
	cur := map[cellKey]int{}
	resv := func(w, d, h int) int {
		k := cellKey{w, d, h}
		if v, ok := cur[k]; ok {
			return v
		}
		v := (m.cfg.Capacity[d][h] - base[k]) / 2
		cur[k] = v
		return v
	}
	all := m.allSeries()

	can := func(a *naiveApp, entrant bool) bool {
		for w := a.start; w <= a.end; w++ {
			occ := m.count(all, w, a.day, a.hour)
			r := resv(w, a.day, a.hour)
			if entrant {
				if occ >= m.cfg.Capacity[a.day][a.hour] || r <= 0 {
					return false
				}
			} else if m.cfg.Capacity[a.day][a.hour]-occ-r <= 0 {
				return false
			}
		}
		return true
	}

	for _, a := range apps {
		if done[a.id] || m.held(a.airline) >= m.cfg.NewEntrantThreshold {
			continue
		}
		if can(a, true) {
			done[a.id] = true
			all = append(all, m.create(a))
			for w := a.start; w <= a.end; w++ {
				k := cellKey{w, a.day, a.hour}
				if cur[k] > 0 {
					cur[k]--
				}
			}
		}
	}
	for _, a := range apps {
		if done[a.id] {
			continue
		}
		if can(a, false) {
			done[a.id] = true
			all = append(all, m.create(a))
		}
	}
	for _, a := range apps {
		if !done[a.id] {
			m.wait[slotKey{a.day, a.hour}] =
				append(m.wait[slotKey{a.day, a.hour}], a)
		}
	}
	m.phase = phaseAllocated
	m.commit(at)
	return nil
}

func (m *NaiveModel) get(sid string) *naiveSeries { return m.series[sid] }

func (m *NaiveModel) ReturnWeek(at time.Time, airline, sid string, week int) (string, error) {
	s := m.get(sid)
	if airline == "" || week < 1 || week > m.cfg.Weeks {
		return "", ErrInvalid
	}
	if err := m.advance(at); err != nil {
		return "", err
	}
	if s == nil || s.airline != airline {
		return "", ErrNotFound
	}
	if week < s.start || week > s.end || s.returned[week] {
		return "", ErrInvalid
	}
	if m.phase == phaseSettled {
		return "", ErrSeasonSettled
	}
	if m.phase != phaseAllocated {
		return "", ErrApplyClosed
	}
	s.returned[week] = true
	s.returnedAt[week] = at
	m.commit(at)
	return m.refill(s.day, s.hour, week), nil
}

func (m *NaiveModel) refill(day, hour, week int) string {
	k := slotKey{day, hour}
	list := m.wait[k]
	for i := 0; i < len(list); {
		a := list[i]
		if week < a.start || week > a.end {
			i++
			continue
		}
		all := m.allSeries()
		ok := true
		for w := a.start; w <= a.end; w++ {
			if m.count(all, w, a.day, a.hour) >= m.cfg.Capacity[a.day][a.hour] {
				ok = false
				break
			}
		}
		if !ok {
			i++
			continue
		}
		m.create(a)
		m.wait[k] = append(list[:i], list[i+1:]...)
		return a.id
	}
	return ""
}

func (m *NaiveModel) ExchangeSeries(at time.Time, alA, sidA, alB, sidB string) error {
	sa, sb := m.get(sidA), m.get(sidB)
	if alA == "" || alB == "" {
		return ErrInvalid
	}
	if err := m.advance(at); err != nil {
		return err
	}
	if sa == nil || sb == nil {
		return ErrNotFound
	}
	if sa == sb || sa.airline != alA || sb.airline != alB ||
		sa.start != sb.start || sa.end != sb.end || alA == alB {
		return ErrInvalid
	}
	if m.phase == phaseSettled {
		return ErrSeasonSettled
	}
	if m.phase != phaseAllocated {
		return ErrApplyClosed
	}
	for _, s := range []*naiveSeries{sa, sb} {
		for _, st := range s.register {
			if st == RegExecuted {
				return ErrWeekExecuted
			}
		}
	}
	sa.airline, sb.airline = sb.airline, sa.airline
	m.commit(at)
	return nil
}

func (m *NaiveModel) RegisterWeek(at time.Time, airline, sid string, week int, st RegStatus) error {
	s := m.get(sid)
	if airline == "" || week < 1 || week > m.cfg.Weeks ||
		(st != RegExecuted && st != RegNotExecuted && st != RegExempt) ||
		at.Before(m.cfg.weekEnd(week)) {
		return ErrInvalid
	}
	if err := m.advance(at); err != nil {
		return err
	}
	if s == nil || s.airline != airline {
		return ErrNotFound
	}
	if week < s.start || week > s.end || s.returned[week] {
		return ErrInvalid
	}
	if m.phase == phaseSettled {
		return ErrSeasonSettled
	}
	if m.phase != phaseAllocated {
		return ErrApplyClosed
	}
	if at.After(m.cfg.weekEnd(week).Add(m.cfg.RegisterWindow)) {
		return ErrRegisterLate
	}
	if _, dup := s.register[week]; dup {
		return ErrDupRegister
	}
	s.register[week] = st
	m.commit(at)
	return nil
}

func (m *NaiveModel) SettleSeason(at time.Time) ([]Qualification, error) {
	if err := m.advance(at); err != nil {
		return nil, err
	}
	if m.phase == phaseSettled {
		return nil, ErrSeasonSettled
	}
	if m.phase != phaseAllocated || at.Before(m.cfg.SeasonEnd) {
		return nil, ErrApplyClosed
	}
	var list []*naiveSeries
	for _, s := range m.series {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
	var qs []Qualification
	for _, s := range list {
		used, planned := 0, 0
		for w := s.start; w <= s.end; w++ {
			if s.returned[w] &&
				(m.cfg.ReturnDeadline.IsZero() || !s.returnedAt[w].After(m.cfg.ReturnDeadline)) {
				continue
			}
			if s.register[w] == RegExempt {
				continue
			}
			planned++
			if s.register[w] == RegExecuted {
				used++
			}
		}
		qs = append(qs, Qualification{
			SeriesID: s.id, Airline: s.airline, Day: s.day, Hour: s.hour,
			StartWeek: s.start, EndWeek: s.end, Used: used, Planned: planned,
			Qualified: qualifies(m.cfg, used, planned),
		})
	}
	m.quals = qs
	m.phase = phaseSettled
	m.commit(at)
	return append([]Qualification(nil), qs...), nil
}

func (m *NaiveModel) Snapshot() Snapshot {
	snap := Snapshot{Series: []SeriesSnap{}, Waitlist: []WaitlistSnap{},
		Qualifications: append([]Qualification(nil), m.quals...)}
	switch m.phase {
	case phaseAccepting:
		snap.Phase = "accepting"
	case phaseAllocated:
		snap.Phase = "allocated"
	case phaseSettled:
		snap.Phase = "settled"
	}
	var list []*naiveSeries
	for _, s := range m.series {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
	for _, s := range list {
		var rs []int
		for w := s.start; w <= s.end; w++ {
			if s.returned[w] {
				rs = append(rs, w)
			}
		}
		reg := map[int]RegStatus{}
		for w, st := range s.register {
			reg[w] = st
		}
		snap.Series = append(snap.Series, SeriesSnap{
			ID: s.id, Airline: s.airline, Day: s.day, Hour: s.hour,
			StartWeek: s.start, EndWeek: s.end, Returned: rs, Register: reg,
		})
	}
	var keys []slotKey
	for k := range m.wait {
		if len(m.wait[k]) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].day != keys[j].day {
			return keys[i].day < keys[j].day
		}
		return keys[i].hour < keys[j].hour
	})
	for _, k := range keys {
		ids := make([]string, 0, len(m.wait[k]))
		for _, a := range m.wait[k] {
			ids = append(ids, a.id)
		}
		snap.Waitlist = append(snap.Waitlist, WaitlistSnap{
			Day: k.day, Hour: k.hour, ApplicationIDs: ids,
		})
	}
	return snap
}
