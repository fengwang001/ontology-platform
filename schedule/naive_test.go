package schedule

import (
	"fmt"
	"sort"
)

// 朴素参照模型：完全按规格逐日扫描实现，不使用 generateInstances，
// 以便与正式实现形成相互独立的对照。
type naiveManager struct {
	series map[string]*naiveSeries
}

type naiveSeries struct {
	id        string
	start     int // ord
	k, nth, w int
	count     int // >0 表示 count 型；==0 表示 until 型
	until     int
	canceled  map[int]bool
	moved     map[int]int
}

func newNaive() *naiveManager { return &naiveManager{series: map[string]*naiveSeries{}} }

// matchNaive 用逐日条件判断 ord 是否为规则候选日：
// 与 start 同月偏移为 k 的非负整数倍、星期为 w、且是该月第 nth 个 w（-1 为最后一个）。
func matchNaive(ord, start, k, nth, w int) bool {
	d, ok := dateFromOrd(ord)
	if !ok {
		return false
	}
	sd, _ := dateFromOrd(start)
	off := (d.year-sd.year)*12 + (d.month - sd.month)
	if off < 0 || off%k != 0 {
		return false
	}
	if d.Weekday() != w {
		return false
	}
	if nth == -1 {
		return d.day+7 > daysInMonth(d.year, d.month)
	}
	return (d.day-1)/7+1 == nth
}

// naiveInstances 从 start 起逐日扫描；count 型收满即止，until 型扫到 until（含）。
// 不存在第 5 个 w 的月份天然没有匹配日：不顺延、不占名额。
func naiveInstances(s *naiveSeries) []int {
	var out []int
	for ord := s.start; ord <= maxDate.ord; ord++ {
		if s.count == 0 && ord > s.until {
			break
		}
		if matchNaive(ord, s.start, s.k, s.nth, s.w) {
			out = append(out, ord)
			if s.count > 0 && len(out) >= s.count {
				break
			}
		}
	}
	return out
}

func (nm *naiveManager) find(id string) (*naiveSeries, bool) {
	s, ok := nm.series[id]
	return s, ok
}

func (nm *naiveManager) create(in CreateInput) ErrorCode {
	if in.Rule.K < 1 {
		return ErrInvalidInterval
	}
	if in.Rule.Nth < -1 || in.Rule.Nth == 0 || in.Rule.Nth > 5 {
		return ErrInvalidNth
	}
	if in.Rule.W < 1 || in.Rule.W > 7 {
		return ErrInvalidWeekday
	}
	start, err := ParseDate(in.Start)
	if err != nil {
		return code(err)
	}
	hasCount := in.Count > 0
	hasUntil := in.Until != ""
	if hasCount == hasUntil {
		return ErrInvalidEnd
	}
	if _, dup := nm.series[in.ID]; dup {
		return ErrDuplicateSeries
	}
	st := &naiveSeries{id: in.ID, start: start.ord, k: in.Rule.K, nth: in.Rule.Nth, w: in.Rule.W,
		canceled: map[int]bool{}, moved: map[int]int{}}
	if hasUntil {
		u, err := ParseDate(in.Until)
		if err != nil {
			return code(err)
		}
		if u.ord < start.ord {
			return ErrUntilBeforeStart
		}
		st.until = u.ord
	} else {
		if in.Count < 1 {
			return ErrInvalidEnd
		}
		st.count = in.Count
	}
	nm.series[in.ID] = st
	return ""
}

func (nm *naiveManager) cancel(id, date string) ErrorCode {
	d, err := ParseDate(date)
	if err != nil {
		return code(err)
	}
	s, ok := nm.find(id)
	if !ok {
		return ErrUnknownSeries
	}
	found := false
	for _, o := range naiveInstances(s) {
		if o == d.ord {
			found = true
		}
	}
	if !found {
		return ErrNotInstance
	}
	if s.canceled[d.ord] {
		return ErrAlreadyCanceled
	}
	s.canceled[d.ord] = true
	delete(s.moved, d.ord)
	return ""
}

func (nm *naiveManager) reschedule(in RescheduleInput) ErrorCode {
	d, err := ParseDate(in.Date)
	if err != nil {
		return code(err)
	}
	nd, err := ParseDate(in.NewDate)
	if err != nil {
		return code(err)
	}
	s, ok := nm.find(in.ID)
	if !ok {
		return ErrUnknownSeries
	}
	var found bool
	for _, o := range naiveInstances(s) {
		if o == d.ord {
			found = true
		}
	}
	if !found {
		return ErrNotInstance
	}
	if s.canceled[d.ord] {
		return ErrAlreadyCanceled
	}
	for _, o := range naiveInstances(s) {
		if o == d.ord || s.canceled[o] {
			continue
		}
		actual := o
		if a, mv := s.moved[o]; mv {
			actual = a
		}
		if actual == nd.ord {
			return ErrDateOccupied
		}
	}
	s.moved[d.ord] = nd.ord
	return ""
}

func (nm *naiveManager) split(in SplitInput) ErrorCode {
	d, err := ParseDate(in.Date)
	if err != nil {
		return code(err)
	}
	if in.NewRule.K < 1 {
		return ErrInvalidInterval
	}
	if in.NewRule.Nth < -1 || in.NewRule.Nth == 0 || in.NewRule.Nth > 5 {
		return ErrInvalidNth
	}
	if in.NewRule.W < 1 || in.NewRule.W > 7 {
		return ErrInvalidWeekday
	}
	s, ok := nm.find(in.ID)
	if !ok {
		return ErrUnknownSeries
	}
	if _, dup := nm.series[in.NewID]; dup {
		return ErrDuplicateSeries
	}
	insts := naiveInstances(s)
	idx := -1
	for i, o := range insts {
		if o == d.ord {
			idx = i
		}
	}
	if idx < 0 {
		return ErrNotInstance
	}

	old := &naiveSeries{id: s.id, start: s.start, k: s.k, nth: s.nth, w: s.w,
		canceled: map[int]bool{}, moved: map[int]int{}}
	if s.count == 0 {
		old.until = d.ord - 1
	} else {
		old.count = idx
	}
	for _, o := range insts[:idx] {
		if s.canceled[o] {
			old.canceled[o] = true
		}
		if a, mv := s.moved[o]; mv {
			old.moved[o] = a
		}
	}

	ns := &naiveSeries{id: in.NewID, start: d.ord, k: in.NewRule.K, nth: in.NewRule.Nth, w: in.NewRule.W,
		canceled: map[int]bool{}, moved: map[int]int{}}
	if s.count == 0 {
		ns.until = s.until
	} else {
		ns.count = s.count - idx
	}
	nm.series[s.id] = old
	nm.series[ns.id] = ns
	return ""
}

func (nm *naiveManager) expand(from, to string) (ErrorCode, []Instance) {
	f, err := ParseDate(from)
	if err != nil {
		return code(err), nil
	}
	t, err := ParseDate(to)
	if err != nil {
		return code(err), nil
	}
	if f.ord >= t.ord {
		return ErrInvalidRange, nil
	}
	if t.ord-f.ord > 3660 {
		return ErrInvalidRange, nil
	}
	var out []Instance
	for _, s := range nm.series {
		for _, o := range naiveInstances(s) {
			if s.canceled[o] {
				continue
			}
			actual := o
			if a, mv := s.moved[o]; mv {
				actual = a
			}
			if actual > f.ord && actual < t.ord {
				od, _ := dateFromOrd(o)
				ad, _ := dateFromOrd(actual)
				out = append(out, Instance{SeriesID: s.id, Original: od, Actual: ad})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Actual.ord != out[j].Actual.ord {
			return out[i].Actual.ord < out[j].Actual.ord
		}
		if out[i].SeriesID != out[j].SeriesID {
			return out[i].SeriesID < out[j].SeriesID
		}
		return out[i].Original.ord < out[j].Original.ord
	})
	return "", out
}

func fmtInstances(ords []int) string {
	out := "["
	for i, o := range ords {
		if i > 0 {
			out += " "
		}
		d, _ := dateFromOrd(o)
		out += d.String()
	}
	return out + "]"
}

func expectCode(t testingT, got error, want ErrorCode, ctx string) {
	t.Helper()
	var gc ErrorCode
	if got != nil {
		gc = code(got)
	}
	if gc != want {
		t.Fatalf("%s: error code=%q want %q", ctx, gc, want)
	}
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

var _ = fmt.Sprintf
