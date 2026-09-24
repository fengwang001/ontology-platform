package interval

import (
	"errors"
	"time"
)

// ErrEmptyInterval 表示空区间（起点等于终点）或零值起点，构造时拒绝。
var ErrEmptyInterval = errors.New("interval: empty or zero-start interval")

// Interval 是左闭右开区间 [Start, End)。End 为零值 time.Time 表示 +∞。
type Interval struct {
	Start time.Time
	End   time.Time
}

// New 构造区间并拒绝空区间。start 必须非零；end 非零时必须晚于 start。
func New(start, end time.Time) (Interval, error) {
	if start.IsZero() || (!end.IsZero() && !start.Before(end)) {
		return Interval{}, ErrEmptyInterval
	}
	return Interval{Start: start, End: end}, nil
}

func before(a, b time.Time) bool { // a < b，b 为零值视为 +∞
	return b.IsZero() || a.Before(b)
}

// Contains 报告 t 是否落在 [Start, End)：起点命中，终点不命中。
func (i Interval) Contains(t time.Time) bool {
	return !t.Before(i.Start) && before(t, i.End)
}

// Empty 报告区间是否为空。
func (i Interval) Empty() bool {
	return i.Start.IsZero() || (!i.End.IsZero() && !i.Start.Before(i.End))
}

// Overlaps 报告两个左闭右开区间是否相交（仅端点相接不算）。
func (i Interval) Overlaps(o Interval) bool {
	return before(i.Start, o.End) && before(o.Start, i.End)
}

// Intersect 返回交集；不相交或交集为空时 ok=false。
func (i Interval) Intersect(o Interval) (Interval, bool) {
	s := i.Start
	if o.Start.After(s) {
		s = o.Start
	}
	e := i.End
	if e.IsZero() || (!o.End.IsZero() && before(o.End, e)) {
		e = o.End
	}
	if !e.IsZero() && !s.Before(e) {
		return Interval{}, false
	}
	return Interval{Start: s, End: e}, true
}

// Minus 返回 i 去掉与 o 交集后的至多两个残余区间（可能为空切片）。
func (i Interval) Minus(o Interval) []Interval {
	out := []Interval{}
	add := func(s, e time.Time) {
		if iv, err := New(s, e); err == nil {
			out = append(out, iv)
		}
	}
	if before(i.Start, o.Start) {
		e := o.Start
		if !i.End.IsZero() && before(i.End, e) {
			e = i.End
		}
		add(i.Start, e)
	}
	if !o.End.IsZero() && (i.End.IsZero() || o.End.Before(i.End)) {
		s := o.End
		if s.Before(i.Start) {
			s = i.Start
		}
		add(s, i.End)
	}
	return out
}
