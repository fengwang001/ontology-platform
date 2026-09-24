// Package api 是对外门面：事务性 Feed、物化视图 View、自检 SelfCheck。
package api

import (
	"fmt"
	"maps"
	"ontology/wagg"
	"ontology/win"
	"reflect"
	"sync"
)

type Event = wagg.Event
type Change = wagg.Change

// 三个互不相同的哨兵错误（互异性由 SelfCheck 与测试钉住）。
var ErrParam, ErrMaxOpen, ErrEmptyKey = wagg.ErrParam, wagg.ErrMaxOpen, wagg.ErrEmptyKey

// ViewKey 是物化视图的键：一个 (Key, 窗口)。
type ViewKey struct {
	Key        string
	Start, End int64
}

// API 线程安全；View/Dropped/SelfCheck 可被多 goroutine 并发调用。
type API struct {
	mu   sync.RWMutex
	agg  *wagg.Aggregator
	view map[ViewKey]int64
}

func New(size, delay, lateness int64, maxOpen int) (*API, error) {
	g, err := wagg.New(size, delay, lateness, maxOpen)
	if err != nil {
		return nil, err
	}
	return &API{agg: g, view: map[ViewKey]int64{}}, nil
}
func (a *API) Feed(evs []Event) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, err := a.agg.Feed(evs)
	if err == nil {
		a.apply(ch)
	}
	return ch, err
}
func (a *API) Flush() []Change {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch := a.agg.Flush()
	a.apply(ch)
	return ch
}
func (a *API) apply(ch []Change) {
	for _, c := range ch {
		k := ViewKey{c.Key, c.Start, c.End}
		if c.Retract {
			a.view[k] -= c.Count
		} else {
			a.view[k] += c.Count
		}
		if a.view[k] == 0 {
			delete(a.view, k)
		}
	}
}
func (a *API) View() map[ViewKey]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return maps.Clone(a.view)
}
func (a *API) Dropped() int64 { a.mu.RLock(); defer a.mu.RUnlock(); return a.agg.Dropped() }
func reference(evs []Event, size, delay, lateness int64) map[ViewKey]int64 {
	wm, seen := int64(0), false
	out := map[ViewKey]int64{}
	for _, e := range evs {
		if w := win.Watermark(e.TS, delay); !seen || w > wm {
			wm, seen = w, true
		}
		if s, end := win.Bounds(e.TS, size); win.Acceptable(wm, end, lateness) {
			out[ViewKey{e.Key, s, end}]++
		}
	}
	return out
}
func replay(log []Change) error {
	cur := map[ViewKey]int64{}
	for i, c := range log {
		k := ViewKey{c.Key, c.Start, c.End}
		if (c.Retract && cur[k] != c.Count) || (!c.Retract && cur[k] != 0) {
			return fmt.Errorf("prefix %d: change %v conflicts with current %d", i, c, cur[k])
		}
		if c.Retract {
			delete(cur, k)
		} else {
			cur[k] = c.Count
		}
	}
	return nil
}

// SelfCheck 用内置事件序列核验四条不变量；不触碰接收者状态，可并发调用。
func (a *API) SelfCheck() error {
	seq := []Event{{Key: "K", TS: 2}, {Key: "K", TS: 7}, {Key: "K", TS: 13}, {Key: "K", TS: 9}, {Key: "K", TS: 18},
		{Key: "K", TS: 4}, {Key: "K", TS: 10}, {Key: "K", TS: 23}, {Key: "a", TS: -25}, {Key: "a", TS: -5},
		{Key: "b", TS: 3}, {Key: "a", TS: 30}}
	inst, _ := New(10, 3, 5, 0) // 参数为合法常量，不会报错
	var log []Change
	prev := inst.agg.Watermark()
	for _, e := range seq { // 逐条喂，同时钉住水位线单调（不变量 3）
		ch, err := inst.Feed([]Event{e})
		if w := inst.agg.Watermark(); err != nil || w < prev {
			return fmt.Errorf("selfcheck: %v / watermark regressed", err)
		} else {
			prev = w
		}
		log = append(log, ch...)
	}
	log = append(log, inst.Flush()...)
	if !reflect.DeepEqual(inst.View(), reference(seq, 10, 3, 5)) { // 不变量 1
		return fmt.Errorf("selfcheck: view != batch recompute")
	}
	if err := replay(log); err != nil { // 不变量 2
		return fmt.Errorf("selfcheck: %w", err)
	}
	if ErrParam == ErrMaxOpen || ErrParam == ErrEmptyKey || ErrMaxOpen == ErrEmptyKey {
		return fmt.Errorf("selfcheck: sentinel errors not distinct")
	}
	for _, p := range [][3]int64{{0, 3, 5}, {10, -1, 5}, {10, 3, -1}} {
		if _, err := New(p[0], p[1], p[2], 0); err != ErrParam {
			return fmt.Errorf("selfcheck: bad params %v: %v", p, err)
		}
	}
	lim, _ := New(10, 3, 5, 1)
	if _, err := lim.Feed([]Event{{Key: "a", TS: 1}}); err != nil {
		return err
	}
	before := lim.View()
	_, e1 := lim.Feed([]Event{{Key: "b", TS: 2}})
	_, e2 := lim.Feed([]Event{{Key: "", TS: 2}})
	if e1 != ErrMaxOpen || e2 != ErrEmptyKey {
		return fmt.Errorf("selfcheck: %v / %v", e1, e2)
	}
	if _, err := lim.Feed([]Event{{Key: "a", TS: 3}}); err != nil ||
		!reflect.DeepEqual(lim.View(), before) || lim.Dropped() != 0 {
		return fmt.Errorf("selfcheck: rejected feed changed state or unusable: %v", err)
	}
	return nil
}
