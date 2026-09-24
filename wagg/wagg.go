// Package wagg 按 (Key, 窗口) 维护计数，推进水位线，产出变更日志。
package wagg

import (
	"errors"
	"math"
	"sort"

	"ontology/win"
)

// 可判定哨兵错误。
var (
	ErrEmptyKey    = errors.New("wagg: empty key")
	ErrTooManyOpen = errors.New("wagg: too many open windows")
)

// Event 是一条带事件时间的上游变更。
type Event struct {
	Key string
	TS  int64
}

// Change 是一条变更日志：Retract 为 true 表示撤回（-），否则为新增（+）。
type Change struct {
	Retract    bool
	Key        string
	Start, End int64
	Count      int64
}

type cell struct {
	count   int64
	emitted int64
	fired   bool
}

// Agg 是窗口聚合器。maxOpen<=0 表示不限制未清除窗口数。
type Agg struct {
	size, delay, lat int64
	maxOpen          int
	wm               win.Watermark
	wins             []win.Window // 按 End 升序
	cells            map[win.Window]map[string]*cell
	dropped          int64
	checked          int // 最近一次水位线推进检查过的未清除窗口数（非导出）
}

// New 构造聚合器；参数合法性由调用方保证。
func New(size, delay, lateness int64, maxOpen int) *Agg {
	return &Agg{size: size, delay: delay, lat: lateness, maxOpen: maxOpen,
		cells: map[win.Window]map[string]*cell{}}
}

// Feed 应用一批事件，返回本批产出的变更日志；任一事件非法则整批不生效。
func (a *Agg) Feed(evs []Event) ([]Change, error) {
	for _, e := range evs {
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
	}
	work := a
	if a.maxOpen > 0 { // 受限时在克隆上试跑，失败即弃，保证不留痕
		work = a.clone()
	}
	out, err := work.apply(evs)
	if err != nil {
		return nil, err
	}
	if work != a {
		*a = *work
	}
	return out, nil
}

func (a *Agg) apply(evs []Event) ([]Change, error) {
	var out []Change
	for _, e := range evs {
		w := win.Of(e.TS, a.size)
		a.wm.Advance(e.TS, a.delay) // 被丢弃的事件同样推进水位线
		switch {
		case !a.wm.Geq(w.End): // 正常计入
			c, err := a.cellOf(w, e.Key)
			if err != nil {
				return nil, err
			}
			c.count++
		case !a.wm.Geq(w.End + a.lat): // 迟到但在 allowedLateness 内：接受并打更新补丁
			c, err := a.cellOf(w, e.Key)
			if err != nil {
				return nil, err
			}
			c.count++
			if c.emitted > 0 {
				out = append(out, Change{true, e.Key, w.Start, w.End, c.emitted})
			}
			out = append(out, Change{false, e.Key, w.Start, w.End, c.count})
			c.emitted, c.fired = c.count, true
		default: // wm >= end+lateness：丢弃
			a.dropped++
		}
		a.sweep(&out)
	}
	return out, nil
}

// sweep 只扫描 End<=wm 的前缀：准点触发（每窗至多一次）并清除 wm>=end+lat 的窗口。
func (a *Agg) sweep(out *[]Change) {
	v, ok := a.wm.Value()
	if !ok {
		a.checked = 0
		return
	}
	purgeBefore := v - a.lat
	i, n := 0, 0
	for i < len(a.wins) && a.wins[i].End <= v {
		n++
		w := a.wins[i]
		m := a.cells[w]
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if c := m[k]; !c.fired {
				*out = append(*out, Change{false, k, w.Start, w.End, c.count})
				c.emitted, c.fired = c.count, true
			}
		}
		if w.End <= purgeBefore {
			delete(a.cells, w)
			a.wins = append(a.wins[:i], a.wins[i+1:]...)
		} else {
			i++
		}
	}
	a.checked = n
}

// cellOf 取 (w,key) 的计数格，必要时按 End 有序插入新窗口。
func (a *Agg) cellOf(w win.Window, key string) (*cell, error) {
	m := a.cells[w]
	if m == nil {
		if a.maxOpen > 0 && len(a.wins) >= a.maxOpen {
			return nil, ErrTooManyOpen
		}
		m = map[string]*cell{}
		a.cells[w] = m
		i := sort.Search(len(a.wins), func(i int) bool { return a.wins[i].End >= w.End })
		a.wins = append(a.wins, win.Window{})
		copy(a.wins[i+1:], a.wins[i:])
		a.wins[i] = w
	}
	if m[key] == nil {
		m[key] = &cell{}
	}
	return m[key], nil
}

func (a *Agg) clone() *Agg {
	b := *a
	b.wins = append([]win.Window(nil), a.wins...)
	b.cells = make(map[win.Window]map[string]*cell, len(a.cells))
	for w, m := range a.cells {
		nm := make(map[string]*cell, len(m))
		for k, c := range m {
			cp := *c
			nm[k] = &cp
		}
		b.cells[w] = nm
	}
	return &b
}

// Flush 把水位线推进到正无穷，触发并清除所有窗口。
func (a *Agg) Flush() []Change {
	a.wm.Force(math.MaxInt64)
	var out []Change
	a.sweep(&out)
	return out
}

// Dropped 返回被丢弃的事件总数。
func (a *Agg) Dropped() int64 { return a.dropped }

// WM 返回当前水位线；ok 为 false 表示负无穷。
func (a *Agg) WM() (v int64, ok bool) { return a.wm.Value() }
