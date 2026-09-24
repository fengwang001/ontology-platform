// Package api 是首值增量维护的对外接口，依赖 first。
package api

import (
	"errors"
	"sync"

	"ontology/evt"
	"ontology/first"
)

var (
	ErrEmptyKey = errors.New("evt: key must not be empty")
	ErrNotFound = errors.New("evt: no such active event to remove")
	ErrCapacity = errors.New("evt: active event count exceeds maxEvents")
)

type Tracker struct {
	mu        sync.RWMutex
	set       *first.Set
	maxEvents int
}

func New(maxEvents int) *Tracker {
	return &Tracker{set: first.NewSet(), maxEvents: maxEvents}
}

func (t *Tracker) Add(Key string, TS int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if Key == "" {
		return ErrEmptyKey
	}
	if t.set.Len() >= t.maxEvents {
		return ErrCapacity
	}
	t.set.Add(evt.Event{Key: Key, TS: TS})
	return nil
}

func (t *Tracker) Remove(Key string, TS int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if Key == "" {
		return ErrEmptyKey
	}
	e := evt.Event{Key: Key, TS: TS}
	if !t.set.Has(e) {
		return ErrNotFound
	}
	t.set.Remove(e)
	return nil
}

// First 返回当前首值；无活跃事件时 ok=false。并发只读安全。
func (t *Tracker) First() (Key string, TS int64, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, good := t.set.First()
	if !good {
		return "", 0, false
	}
	return e.Key, e.TS, true
}
func (t *Tracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.set.Len()
}

// SelfCheck 对内置操作序列核验四条不变量，全部成立返回 true。
func (t *Tracker) SelfCheck() bool {
	c := New(8)
	ref := map[evt.Event]int{}
	type op struct {
		add bool
		k   string
		ts  int64
	}
	ops := []op{ // 第三节八步
		{true, "a", 5}, {true, "b", 5}, {true, "a", 3}, {true, "a", 3},
		{false, "a", 3}, {false, "a", 3}, {false, "a", 5}, {false, "b", 5},
	}
	for _, o := range ops {
		e := evt.Event{Key: o.k, TS: o.ts}
		if o.add {
			if c.Add(o.k, o.ts) != nil {
				return false
			}
			ref[e]++
		} else if c.Remove(o.k, o.ts) != nil {
			return false
		} else {
			ref[e]--
			if ref[e] == 0 {
				delete(ref, e)
			}
		}
		k, ts, ok := c.First() // 不变量 1/2/3：每步逐字段对拍朴素重扫
		var g evt.Event
		gok := false
		for e := range ref {
			if !gok || evt.Less(e, g) {
				g, gok = e, true
			}
		}
		if ok != gok || (ok && (k != g.Key || ts != g.TS)) {
			return false
		}
	}
	return c.checkRejectedAtomic() && c.checkMultiset()
}

func (c *Tracker) checkRejectedAtomic() bool {
	if ErrEmptyKey == ErrNotFound || ErrEmptyKey == ErrCapacity || ErrNotFound == ErrCapacity {
		return false
	}
	if c.Add("", 1) != ErrEmptyKey || c.Remove("zzz", -9) != ErrNotFound || c.Count() != 0 {
		return false
	}
	if _, _, ok := c.First(); ok { // 八步后为空，被拒后仍须为空
		return false
	}
	d := New(1)
	if d.Add("x", 1) != nil || d.Add("y", 2) != ErrCapacity || d.Count() != 1 {
		return false
	}
	if d.Remove("x", 1) != nil || d.Add("y", 2) != nil {
		return false
	}
	k, ts, ok := d.First()
	return ok && k == "y" && ts == 2
}

func (c *Tracker) checkMultiset() bool {
	d := New(100)
	const K = 5
	for i := 1; i <= K; i++ {
		if d.Add("dup", 7) != nil || d.Count() != i {
			return false
		}
	}
	for i := K; i >= 1; i-- {
		if d.Remove("dup", 7) != nil || d.Count() != i-1 {
			return false
		}
	}
	_, _, ok := d.First()
	return !ok
}
