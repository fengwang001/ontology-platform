// Package api 是翻滚窗口计数的对外入口，仅依赖 wagg；状态全部在进程内存。
package api

import "errors"
import "fmt"
import "math/rand"
import "slices"
import "ontology/wagg"
import "ontology/win"

type Event = wagg.Event
type Change = wagg.Change
type ViewKey = wagg.ViewKey

// 三类可判定哨兵错误互不相同；ErrSelfCheck 表示自检失败，errors.Is 可判定。
var ErrInvalidParams = wagg.ErrInvalidParams
var ErrTooManyOpen = wagg.ErrTooManyOpen
var ErrEmptyKey = wagg.ErrEmptyKey
var ErrSelfCheck = errors.New("api: self-check failed")
var errsDistinct = ErrInvalidParams != ErrTooManyOpen && ErrTooManyOpen != ErrEmptyKey && ErrInvalidParams != ErrEmptyKey

type Engine struct{ a *wagg.Agg }

// New 构造实例；size 非正或 delay/lateness 为负返回 ErrInvalidParams（不留任何状态）。
func New(size, delay, lateness int64, maxOpen int) (*Engine, error) {
	a, err := wagg.NewAgg(size, delay, lateness, maxOpen)
	if err != nil {
		return nil, err
	}
	return &Engine{a: a}, nil
}
func (e *Engine) Feed(evs []Event) ([]Change, error) { return e.a.Feed(evs) }
func (e *Engine) Flush() []Change                    { return e.a.Flush() }
func (e *Engine) View() map[ViewKey]int64            { return e.a.View() }
func (e *Engine) Dropped() int64                     { return e.a.Dropped() }

// oracle 是独立参考实现：按规则只统计「被接受」事件，分组计数（含负时间戳）。
func oracle(size, delay, lateness int64, evs []Event) map[ViewKey]int64 {
	mx := int64(-1 << 63)
	got := map[ViewKey]int64{}
	for _, e := range evs {
		mx = max(mx, e.TS)
		w := win.Of(e.TS, size)
		wm := win.SatAdd(mx, -delay)
		if win.IsLate(w.End, true, wm) && !win.AcceptLate(w.End, lateness, true, wm) {
			continue
		}
		got[ViewKey{Key: e.Key, Start: w.Start, End: w.End}]++
	}
	return got
}

// prefixOK 顺序折叠变更日志：任一槽位至多一个现值，每条 - 恰好撤回现值，终态等于 view。
func prefixOK(log []Change, view map[ViewKey]int64) error {
	cur := map[ViewKey]int64{}
	for _, c := range log {
		vk := ViewKey{Key: c.Key, Start: c.Start, End: c.End}
		if c.Plus {
			cur[vk] = c.Count
		} else if v, ok := cur[vk]; !ok || v != c.Count {
			return fmt.Errorf("minus %d does not retract current (ok=%v)", c.Count, ok)
		} else {
			delete(cur, vk)
		}
	}
	if !mapsEq(cur, view) {
		return fmt.Errorf("folded %v != view %v", cur, view)
	}
	return nil
}

var eightWant = map[int][]Change{
	2: {{Plus: true, Key: "K", Start: 0, End: 10, Count: 2}},
	3: {{Plus: false, Key: "K", Start: 0, End: 10, Count: 2}, {Plus: true, Key: "K", Start: 0, End: 10, Count: 3}},
	7: {{Plus: true, Key: "K", Start: 10, End: 20, Count: 3}},
}

// SelfCheck 对内置序列核验四条不变量；任一失败返回包了 ErrSelfCheck 的可判定错误。
func (e *Engine) SelfCheck() error {
	en, _ := New(10, 3, 5, 1000)
	var full []Change
	evs := make([]Event, 0, 8)
	for i, t := range []int64{2, 7, 13, 9, 18, 4, 10, 23} {
		ch, err := en.Feed([]Event{{Key: "K", TS: t}})
		full = append(full, ch...)
		evs = append(evs, Event{Key: "K", TS: t})
		if err != nil || !slices.Equal(ch, eightWant[i]) || prefixOK(full, en.View()) != nil {
			return fail("step %d ch=%v err=%v", i, ch, err)
		}
	}
	full = append(full, en.Flush()...)
	want := oracle(10, 3, 5, evs)
	if !mapsEq(en.View(), want) || en.Dropped() != 1 || prefixOK(full, en.View()) != nil {
		return fail("view=%v want=%v dropped=%d", en.View(), want, en.Dropped())
	}
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 25; iter++ {
		ord := slices.Clone(evs)
		r.Shuffle(len(ord), func(i, j int) { ord[i], ord[j] = ord[j], ord[i] })
		g, _ := New(10, 3, 5, 1000)
		lg, _ := g.Feed(ord)
		lg = append(lg, g.Flush()...)
		if !mapsEq(g.View(), oracle(10, 3, 5, ord)) || prefixOK(lg, g.View()) != nil {
			return fail("iter %d order %v view/prefix mismatch", iter, ord)
		}
	}
	mono, _ := New(10, 3, 5, 1000)
	mono.Feed([]Event{{Key: "K", TS: 100}, {Key: "K", TS: 50}})
	if mono.Dropped() != 1 {
		return fail("discarded event watermark rule: dropped=%d", mono.Dropped())
	}
	is := func(err, target error) bool { return errors.Is(err, target) }
	if _, p0 := New(0, 3, 5, 10); !is(p0, ErrInvalidParams) {
		return fail("size<=0: %v", p0)
	}
	if _, p1 := New(10, -1, 5, 10); !is(p1, ErrInvalidParams) {
		return fail("delay<0: %v", p1)
	}
	bad, _ := New(10, 3, 5, 1)
	bad.Feed([]Event{{Key: "K", TS: 2}})
	if _, x := bad.Feed([]Event{{Key: "K2", TS: 2}}); !is(x, ErrTooManyOpen) {
		return fail("maxOpen: %v", x)
	}
	if _, x := bad.Feed([]Event{{Key: "", TS: 2}}); !is(x, ErrEmptyKey) {
		return fail("empty key: %v", x)
	}
	if bad.Dropped() != 0 || len(bad.View()) != 0 || !errsDistinct {
		return fail("rejected batch left state: view=%v dropped=%d", bad.View(), bad.Dropped())
	}
	if _, err := bad.Feed([]Event{{Key: "K", TS: 3}}); err != nil {
		return fail("engine unusable after rejection: %v", err)
	}
	return nil
}

func mapsEq(a, b map[ViewKey]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
func fail(f string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSelfCheck, fmt.Sprintf(f, args...))
}
