package schedule

import (
	"fmt"
	"sort"
	"time"
)

// 本文件提供一个“朴素参考实现”：
// 逐日历日扫描、对每个日期独立判断它在所在月中是第几个星期几（含 -1=最后一个），
// 从而避免与生产代码共享任何候选生成逻辑，用于差分对照测试。

var (
	naiveMin = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	naiveMax = time.Date(2200, 12, 31, 0, 0, 0, 0, time.UTC)
)

func naiveParse(s string) (time.Time, ErrCode) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil || len(s) != 10 {
		return time.Time{}, ErrDateFormat
	}
	if t.Before(naiveMin) || t.After(naiveMax) {
		return time.Time{}, ErrDateRange
	}
	if t.Format("2006-01-02") != s {
		return time.Time{}, ErrDateRange
	}
	return t, ""
}

func naiveIsoWeekday(t time.Time) int {
	d := int(t.Weekday()) // Sunday=0
	if d == 0 {
		return 7
	}
	return d
}

// nthInMonth 返回 t 所在月中，t 是同星期几里的第几个（从 1 起），以及该月同星期几总数。
func nthInMonth(t time.Time) (int, int) {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	dim := first.AddDate(0, 1, -1).Day()
	w := naiveIsoWeekday(t)
	firstAt := 1 + (w-naiveIsoWeekday(first)+7)%7
	nth := (t.Day()-firstAt)/7 + 1
	total := (dim-firstAt)/7 + 1
	return nth, total
}

type naiveEx struct {
	cancelled  bool
	reschedule time.Time
}

type naiveSeries struct {
	id      string
	start   time.Time
	rule    Rule
	isCount bool
	count   int
	until   time.Time
	ex      map[string]*naiveEx
}

type naiveModel struct {
	series map[string]*naiveSeries
}

func newNaiveModel() *naiveModel { return &naiveModel{series: map[string]*naiveSeries{}} }

func naiveTerm(t Termination, start time.Time) (bool, int, time.Time, ErrCode) {
	hasCount := t.Count != 0
	hasUntil := t.Until != ""
	if hasCount == hasUntil {
		return false, 0, time.Time{}, ErrTerminalCountUntil
	}
	if hasCount {
		if t.Count < 1 {
			return false, 0, time.Time{}, ErrCountRange
		}
		return true, t.Count, time.Time{}, ""
	}
	u, code := naiveParse(t.Until)
	if code != "" {
		return false, 0, time.Time{}, code
	}
	if u.Before(start) {
		return false, 0, time.Time{}, ErrUntilBeforeStart
	}
	return false, 0, u, ""
}

// naiveGenerate 逐日历日扫描生成全部实例原日期。
func naiveGenerate(s *naiveSeries) []time.Time {
	if s.isCount && s.count == 0 {
		return nil
	}
	var out []time.Time
	limit := naiveMax
	if !s.isCount {
		limit = s.until
	}
	sy, sm, _ := s.start.Date()
	for d := s.start; !d.After(limit) && !d.After(naiveMax); d = d.AddDate(0, 0, 1) {
		y, m, _ := d.Date()
		// start 所在月为第 0 个月，之后每隔 k 个月。
		months := (y-sy)*12 + int(m) - int(sm)
		if months < 0 || months%s.rule.IntervalMonths != 0 {
			continue
		}
		if naiveIsoWeekday(d) != s.rule.Weekday {
			continue
		}
		nth, total := nthInMonth(d)
		if s.rule.Nth == -1 {
			if nth != total {
				continue
			}
		} else if nth != s.rule.Nth {
			continue
		}
		if d.Before(s.start) {
			continue
		}
		out = append(out, d)
		if s.isCount && len(out) == s.count {
			break
		}
	}
	return out
}

func (mm *naiveModel) create(in CreateInput) ErrCode {
	start, code := naiveParse(in.Start)
	if code != "" {
		return code
	}
	if code := naiveValidateRule(in.Rule); code != "" {
		return code
	}
	isCount, count, until, code := naiveTerm(in.Term, start)
	if code != "" {
		return code
	}
	if _, ok := mm.series[in.ID]; ok {
		return ErrDuplicateID
	}
	mm.series[in.ID] = &naiveSeries{
		id: in.ID, start: start, rule: in.Rule,
		isCount: isCount, count: count, until: until,
		ex: map[string]*naiveEx{},
	}
	return ""
}

func naiveValidateRule(r Rule) ErrCode {
	if r.IntervalMonths < 1 {
		return ErrBadInterval
	}
	if r.Nth < -1 || r.Nth == 0 || r.Nth > 5 {
		return ErrBadNth
	}
	if r.Weekday < 1 || r.Weekday > 7 {
		return ErrBadWeekday
	}
	return ""
}

func (mm *naiveModel) lookup(id string, ds string) (*naiveSeries, time.Time, ErrCode) {
	s, ok := mm.series[id]
	if !ok {
		return nil, time.Time{}, ErrUnknownSeries
	}
	d, code := naiveParse(ds)
	if code != "" {
		return nil, time.Time{}, code
	}
	for _, o := range naiveGenerate(s) {
		if o.Equal(d) {
			return s, d, ""
		}
	}
	return nil, time.Time{}, ErrNotInstance
}

func (mm *naiveModel) cancel(id, ds string) ErrCode {
	s, d, code := mm.lookup(id, ds)
	if code != "" {
		return code
	}
	key := d.Format("2006-01-02")
	if x := s.ex[key]; x != nil && x.cancelled {
		return ErrAlreadyCancelled
	}
	s.ex[key] = &naiveEx{cancelled: true}
	return ""
}

func (mm *naiveModel) actual(s *naiveSeries, orig time.Time) time.Time {
	if x := s.ex[orig.Format("2006-01-02")]; x != nil && !x.cancelled && !x.reschedule.IsZero() {
		return x.reschedule
	}
	return orig
}

func (mm *naiveModel) reschedule(id, ds, ts string) ErrCode {
	s, d, code := mm.lookup(id, ds)
	if code != "" {
		return code
	}
	tgt, c := naiveParse(ts)
	if c != "" {
		return c
	}
	key := d.Format("2006-01-02")
	if x := s.ex[key]; x != nil && x.cancelled {
		return ErrAlreadyCancelled
	}
	for _, o := range naiveGenerate(s) {
		if o.Equal(d) {
			continue
		}
		x := s.ex[o.Format("2006-01-02")]
		if x != nil && x.cancelled {
			continue
		}
		if mm.actual(s, o).Equal(tgt) {
			return ErrTargetOccupied
		}
	}
	if x := s.ex[key]; x != nil {
		x.reschedule = tgt
	} else {
		s.ex[key] = &naiveEx{reschedule: tgt}
	}
	return ""
}

func (mm *naiveModel) split(in SplitInput) ErrCode {
	s, d, code := mm.lookup(in.ID, in.Date)
	if code != "" {
		return code
	}
	if code := naiveValidateRule(in.NewRule); code != "" {
		return code
	}
	if _, ok := mm.series[in.NewID]; ok {
		return ErrDuplicateID
	}
	var before int
	for _, o := range naiveGenerate(s) {
		if o.Before(d) {
			before++
		}
	}
	for k := range s.ex {
		t, _ := time.Parse("2006-01-02", k)
		if !t.Before(d) {
			delete(s.ex, k)
		}
	}
	wasCount := s.isCount
	oldCount := s.count
	ns := &naiveSeries{id: in.NewID, start: d, rule: in.NewRule, ex: map[string]*naiveEx{}}
	if wasCount {
		s.count = before
		s.isCount = true
		ns.isCount = true
		ns.count = oldCount - before
	} else {
		ns.until = s.until
		s.until = d.AddDate(0, 0, -1)
	}
	mm.series[in.NewID] = ns
	return ""
}

func (mm *naiveModel) expand(fromS, toS string) ([]Instance, ErrCode) {
	f, code := naiveParse(fromS)
	if code != "" {
		return nil, code
	}
	t, c := naiveParse(toS)
	if c != "" {
		return nil, c
	}
	if !f.Before(t) {
		return nil, ErrEmptyRange
	}
	if t.Sub(f).Hours()/24 > 3660 {
		return nil, ErrRangeTooWide
	}
	var out []Instance
	for _, s := range mm.series {
		for _, orig := range naiveGenerate(s) {
			x := s.ex[orig.Format("2006-01-02")]
			if x != nil && x.cancelled {
				continue
			}
			actual := mm.actual(s, orig)
			if actual.Before(f) || !actual.Before(t) {
				continue
			}
			out = append(out, Instance{
				SeriesID: s.id,
				Original: orig.Format("2006-01-02"),
				Actual:   actual.Format("2006-01-02"),
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
	return out, ""
}

func dumpInstances(ins []Instance) string { return fmt.Sprintf("%v", ins) }
