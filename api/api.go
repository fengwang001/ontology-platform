// Package api 是对外的并发安全入口，依赖 wagg（wagg 再依赖 win）。
package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"

	"ontology/wagg"
	"ontology/win"
)

var ErrInvalidParams = errors.New("api: size 必须为正，delay/lateness 不可为负")
var ErrEmptyKey = wagg.ErrEmptyKey
var ErrMaxOpen = wagg.ErrTooManyOpen
var errSelfCheck = errors.New("api: SelfCheck 失败")

type Event = wagg.Event
type Change = wagg.Change
type WindowCount struct{ Start, End, Count int64 }

// Engine 状态全在进程内存；读方法可并发，读写之间互斥。
type Engine struct {
	mu  sync.RWMutex
	agg *wagg.Agg
}

func New(size, delay, lateness int64, maxOpen int) (*Engine, error) {
	if size <= 0 || delay < 0 || lateness < 0 {
		return nil, ErrInvalidParams
	}
	return &Engine{agg: wagg.New(size, delay, lateness, maxOpen)}, nil
}
func (e *Engine) Feed(evs []Event) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.agg.Feed(evs)
}
func (e *Engine) Dropped() int64  { e.mu.RLock(); defer e.mu.RUnlock(); return e.agg.Dropped() }
func (e *Engine) Flush() []Change { e.mu.Lock(); defer e.mu.Unlock(); return e.agg.Flush() }

// View 返回下游顺序应用全部已提交变更日志后的物化视图（每键按起点有序）。
func (e *Engine) View() map[string][]WindowCount {
	e.mu.RLock()
	log := e.agg.Log()
	e.mu.RUnlock()
	cur := map[string]map[int64]WindowCount{}
	for _, c := range log {
		if c.Plus {
			bag(cur, c.Key)[c.Start] = WindowCount{c.Start, c.End, c.Count}
		} else {
			delete(cur[c.Key], c.Start)
		}
	}
	return sorted(cur)
}
func bag[T any](m map[string]map[int64]T, k string) map[int64]T {
	if m[k] == nil {
		m[k] = map[int64]T{}
	}
	return m[k]
}
func sorted(cur map[string]map[int64]WindowCount) map[string][]WindowCount {
	out := make(map[string][]WindowCount, len(cur))
	for k, b := range cur {
		s := make([]WindowCount, 0, len(b))
		for _, v := range b {
			s = append(s, v)
		}
		sort.Slice(s, func(i, j int) bool { return s[i].Start < s[j].Start })
		out[k] = s
	}
	return out
}

// refBatch 是独立参考重算：用 win 规则只数「被接受」的事件。
func refBatch(size, delay, lateness int64, evs []Event) map[string][]WindowCount {
	var wm win.Watermark
	cur := map[string]map[int64]WindowCount{}
	for _, ev := range evs {
		wd, wmv := win.Assign(ev.TS, size), wm.Observe(ev.TS, delay)
		if win.DropLate(wmv, wd, lateness) {
			continue
		}
		b := bag(cur, ev.Key)
		b[wd.Start] = WindowCount{wd.Start, wd.End, b[wd.Start].Count + 1}
	}
	return sorted(cur)
}

// pushAll 顺序应用一批变更（每条之后即一个前缀）；撤回缺失或不等值时返回 false。
func pushAll(cur map[string]map[int64]int64, cs []Change) bool {
	for _, c := range cs {
		b := bag(cur, c.Key)
		if c.Plus {
			b[c.Start] = c.Count
			continue
		}
		v, ok := b[c.Start]
		if !ok || v != c.Count {
			return false
		}
		delete(b, c.Start)
	}
	return true
}

// SelfCheck 用内置序列核验四条不变量；只构造局部实例，可并发调用。
func SelfCheck() error {
	traces := [][]Event{{{Key: "K", TS: 2}, {Key: "K", TS: 9}, {Key: "K", TS: 15}, {Key: "K", TS: 4}, {Key: "K", TS: 18}, {Key: "K", TS: 5}, {Key: "K", TS: 22}, {Key: "K", TS: 7}}}
	r := rand.New(rand.NewSource(1))
	for g := 0; g < 8; g++ { // 多键、随机顺序、含负时间戳
		t := make([]Event, 40)
		for i := range t {
			t[i] = Event{Key: string(rune('a' + r.Intn(4))), TS: int64(r.Intn(61)) - 20}
		}
		traces = append(traces, t)
	}
	for _, evs := range traces {
		e, _ := New(10, 3, 5, 0)
		cur := map[string]map[int64]int64{}
		for _, ev := range evs { // 不变量 2：每个前缀都自洽（输入固定合法，err 必为 nil）
			cs, _ := e.Feed([]Event{ev})
			if !pushAll(cur, cs) {
				return errSelfCheck
			}
		}
		if !pushAll(cur, e.Flush()) || !reflect.DeepEqual(e.View(), refBatch(10, 3, 5, evs)) { // 不变量 1
			return errSelfCheck
		}
	}
	e, _ := New(10, 0, 0, 0) // 不变量 3：先高后低，水位线不回退，低 TS 必被丢
	e.Feed([]Event{{Key: "K", TS: 100}, {Key: "K", TS: 0}})
	e.Flush()
	if e.Dropped() != 1 || len(e.View()["K"]) != 1 {
		return errSelfCheck
	}
	a := wagg.New(2, 0, 0, 1) // 不变量 4：被拒批次不留痕且实例仍可继续使用
	a.Feed([]Event{{Key: "a", TS: 0}})
	before := len(a.Log())
	if _, err := a.Feed([]Event{{Key: "b", TS: 2}, {Key: "", TS: 4}}); !errors.Is(err, wagg.ErrEmptyKey) {
		return errSelfCheck
	}
	if _, err := a.Feed([]Event{{Key: "a", TS: 1}}); err != nil || len(a.Log()) != before || a.Dropped() != 0 {
		return errSelfCheck
	}
	return nil
}
